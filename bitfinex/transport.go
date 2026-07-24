package bitfinex

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/bitfinexcom/bitfinex-api-go/v2/websocket"
	gorillaws "github.com/gorilla/websocket"
)

type websocketFactory struct {
	url     string
	proxy   *url.URL
	timeout time.Duration
}

func newWebsocketFactory(endpoint string, proxy *url.URL, timeout time.Duration) websocket.AsynchronousFactory {
	return &websocketFactory{url: endpoint, proxy: proxy, timeout: timeout}
}

func (f *websocketFactory) Create() websocket.Asynchronous {
	return &websocketTransport{
		url:     f.url,
		proxy:   f.proxy,
		timeout: f.timeout,
		listen:  make(chan []byte, 128),
		done:    make(chan error, 1),
		closed:  make(chan struct{}),
	}
}

type websocketTransport struct {
	url     string
	proxy   *url.URL
	timeout time.Duration

	mu        sync.Mutex
	conn      *gorillaws.Conn
	listen    chan []byte
	done      chan error
	closed    chan struct{}
	closeOnce sync.Once
	doneOnce  sync.Once
}

func (w *websocketTransport) Connect() error {
	dialer := &gorillaws.Dialer{
		Proxy:            http.ProxyURL(w.proxy),
		HandshakeTimeout: w.timeout,
		Subprotocols:     []string{"p1", "p2"},
		ReadBufferSize:   1024,
		WriteBufferSize:  1024,
	}
	ctx, cancel := context.WithTimeout(context.Background(), w.timeout)
	defer cancel()
	conn, response, err := dialer.DialContext(ctx, w.url, nil)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.conn = conn
	w.mu.Unlock()
	go w.readLoop()
	go w.pingLoop()
	return nil
}

func (w *websocketTransport) Send(ctx context.Context, message interface{}) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-w.closed:
		return errors.New("bitfinex websocket connection closed")
	default:
	}

	w.mu.Lock()
	if w.conn == nil {
		w.mu.Unlock()
		return errors.New("bitfinex websocket is not connected")
	}
	deadline := time.Now().Add(w.timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err = w.conn.SetWriteDeadline(deadline); err != nil {
		w.mu.Unlock()
		return err
	}
	err = w.conn.WriteMessage(gorillaws.TextMessage, data)
	w.mu.Unlock()
	if err != nil {
		w.fail(err)
	}
	return err
}

func (w *websocketTransport) Listen() <-chan []byte {
	return w.listen
}

func (w *websocketTransport) Close() {
	w.closeOnce.Do(func() {
		close(w.closed)
		w.mu.Lock()
		if w.conn != nil {
			_ = w.conn.WriteControl(
				gorillaws.CloseMessage,
				gorillaws.FormatCloseMessage(gorillaws.CloseNormalClosure, ""),
				time.Now().Add(time.Second),
			)
			_ = w.conn.Close()
		}
		w.mu.Unlock()
	})
	w.signalDone(gorillaws.ErrCloseSent)
}

func (w *websocketTransport) Done() <-chan error {
	return w.done
}

func (w *websocketTransport) readLoop() {
	for {
		w.mu.Lock()
		conn := w.conn
		w.mu.Unlock()
		if conn == nil {
			w.signalDone(errors.New("bitfinex websocket is not connected"))
			return
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			w.fail(err)
			return
		}
		select {
		case w.listen <- data:
		case <-w.closed:
			return
		}
	}
}

func (w *websocketTransport) pingLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			w.mu.Lock()
			var err error
			if w.conn != nil {
				err = w.conn.WriteControl(gorillaws.PingMessage, nil, time.Now().Add(w.timeout))
			}
			w.mu.Unlock()
			if err != nil {
				w.fail(err)
				return
			}
		case <-w.closed:
			return
		}
	}
}

func (w *websocketTransport) signalDone(err error) {
	w.doneOnce.Do(func() {
		w.done <- err
	})
}

func (w *websocketTransport) fail(err error) {
	w.signalDone(err)
	w.closeOnce.Do(func() {
		close(w.closed)
		w.mu.Lock()
		if w.conn != nil {
			_ = w.conn.Close()
		}
		w.mu.Unlock()
	})
}
