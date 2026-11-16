package host

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/EternisAI/takes-replies-basket-test/internal/takeintelligence"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestHyperliquidLivePublicCatalogSmoke(t *testing.T) {
	if os.Getenv("TAKES_REPLY_LIVE_CATALOG") != "1" {
		t.Skip("set TAKES_REPLY_LIVE_CATALOG=1 for one bounded public catalogue read")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	snapshot, err := NewHyperliquidCatalogReader(&http.Client{Timeout: 10 * time.Second}, []string{"xyz"}).CaptureCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("read-only Hyperliquid catalog: provider=%s result=%s venues=%d instruments=%d digest=%s", snapshot.Provider, snapshot.Result, len(snapshot.ConfiguredVenues), len(snapshot.Instruments), snapshot.Digest)
}

func TestHyperliquidCatalogPreservesExactReadOnlyVenueMapping(t *testing.T) {
	requests := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Method != http.MethodPost || r.URL.String() != defaultHyperliquidInfoURL {
			return nil, errors.New("unexpected market request " + r.Method + " " + r.URL.String())
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, err
		}
		response := func(data string) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(data)), Request: r}, nil
		}
		if body["type"] == "perpDexs" {
			return response(`[null,{"name":"xyz"}]`)
		}
		if body["type"] == "perpConciseAnnotations" {
			return response(`[["xyz:TSLA",{"category":"stocks","displayName":"Tesla, Inc.","keywords":["electric vehicles"]}]]`)
		}
		if body["type"] == "perpsAtOpenInterestCap" {
			return response(`[]`)
		}
		if body["type"] == "perpDexLimits" {
			return response(`{"coinToOiCap":[["xyz:TSLA","5000000"]]}`)
		}
		if body["type"] != "metaAndAssetCtxs" {
			return nil, errors.New("unexpected Hyperliquid info operation " + body["type"])
		}
		if body["dex"] == "xyz" {
			return response(`[{"universe":[{"name":"xyz:TSLA","maxLeverage":5,"marginMode":"normal","growthMode":"enabled","deployerFeeScale":"0.1"}],"collateralToken":0},[{"funding":"0.0001","openInterest":"100","dayNtlVlm":"5000000","markPx":"300","oraclePx":"299"}]]`)
		}
		return response(`[{"universe":[{"name":"BTC","maxLeverage":50}],"collateralToken":0},[{"funding":"0.00001","openInterest":"10","dayNtlVlm":"1000000","markPx":"60000","oraclePx":"59900"}]]`)
	})

	reader := NewHyperliquidCatalogReader(&http.Client{Transport: transport}, []string{"xyz"})
	snapshot, err := reader.CaptureCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Result != takeintelligence.SnapshotComplete || snapshot.Provider != "hyperliquid" || len(snapshot.Instruments) != 2 {
		t.Fatalf("unexpected snapshot header: %+v", snapshot)
	}
	if got, err := takeintelligence.CatalogDigest(snapshot); err != nil || got != snapshot.Digest {
		t.Fatalf("snapshot digest invalid: %s %v", got, err)
	}
	var stock *takeintelligence.Instrument
	for index := range snapshot.Instruments {
		if snapshot.Instruments[index].Key.Symbol == "xyz:TSLA" {
			stock = &snapshot.Instruments[index]
		}
	}
	if stock == nil {
		t.Fatal("exact HIP-3 stock listing missing")
	}
	if stock.Key.Venue != "xyz" || stock.Key.Product != "perpetual" || stock.DisplayName != "Tesla, Inc." || stock.Category != "stocks" {
		t.Fatalf("listing identity changed: %+v", stock)
	}
	if !stock.AtOICapKnown || stock.AtOICap || !stock.GrowthModeKnown || !stock.GrowthMode || stock.ObservedAt.IsZero() {
		t.Fatalf("public market state lost its known/unknown boundary: %+v", stock)
	}
	if len(snapshot.ConfiguredVenues) != 2 || requests < 6 {
		t.Fatalf("read sources/venues = %d/%v", requests, snapshot.ConfiguredVenues)
	}
}
