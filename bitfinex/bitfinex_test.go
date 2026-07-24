package bitfinex

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/viper"
	"github.com/ztrade/exchange"
)

func TestNewBitfinexSelectsSpotKindAndDecodesSnakeCaseConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conf/pub:list:pair:exchange" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]interface{}{[]string{"BTCUST"}})
	}))
	defer server.Close()

	config := viper.New()
	config.Set("exchanges.bfx.kind", "spot")
	config.Set("exchanges.bfx.currency", "USDT")
	config.Set("exchanges.bfx.rest_url", server.URL)
	client, err := NewBitfinex(exchange.WrapViper(config), "bfx")
	if err != nil {
		t.Fatalf("new bitfinex: %v", err)
	}
	defer client.Stop()
	bitfinexClient, ok := client.(*Client)
	if !ok {
		t.Fatalf("client type = %T, want *bitfinex.Client", client)
	}
	if bitfinexClient.kind != KindSpot {
		t.Fatalf("client kind = %q, want %q", bitfinexClient.kind, KindSpot)
	}
}
