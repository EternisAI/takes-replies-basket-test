package host

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/EternisAI/takes-replies-basket-test/internal/takeintelligence"
	"github.com/google/uuid"
)

const (
	defaultHyperliquidInfoURL = "https://api.hyperliquid.xyz/info"
	maxHyperliquidBody        = 4 << 20
	maxHyperliquidCatalogAge  = 10 * time.Second
	hip3AssetBase             = 100000
	hip3DexStride             = 10000
)

// HyperliquidCatalogReader is a public, read-only adapter. It calls only the
// venue's /info endpoint for metadata and market contexts. It has no wallet,
// account, order, signing, or trading methods.
type HyperliquidCatalogReader struct {
	client *http.Client
	url    string
	dexes  []string
}

func NewHyperliquidCatalogReader(client *http.Client, dexes []string) *HyperliquidCatalogReader {
	if client == nil {
		client = &http.Client{Timeout: maxHyperliquidCatalogAge}
	}
	if client.Timeout == 0 {
		copy := *client
		copy.Timeout = maxHyperliquidCatalogAge
		client = &copy
	}
	clientCopy := *client
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HyperliquidCatalogReader{client: &clientCopy, url: defaultHyperliquidInfoURL, dexes: normalizeDexes(dexes)}
}

func newHyperliquidCatalogReader(client *http.Client, infoURL string, dexes []string) *HyperliquidCatalogReader {
	r := NewHyperliquidCatalogReader(client, dexes)
	if strings.TrimSpace(infoURL) != "" {
		r.url = infoURL
	}
	return r
}

func normalizeDexes(dexes []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, dex := range dexes {
		dex = strings.TrimSpace(dex)
		if dex != "" && !seen[dex] {
			seen[dex] = true
			out = append(out, dex)
		}
	}
	sort.Strings(out)
	return out
}

func (r *HyperliquidCatalogReader) CaptureCatalog(ctx context.Context) (takeintelligence.CatalogSnapshot, error) {
	if r == nil || r.client == nil {
		return takeintelligence.CatalogSnapshot{}, errors.New("Hyperliquid catalog reader is unavailable")
	}
	started := time.Now().UTC()
	var dexNames []*struct {
		Name string `json:"name"`
	}
	if len(r.dexes) > 0 {
		if err := r.post(ctx, map[string]string{"type": "perpDexs"}, &dexNames); err != nil {
			return takeintelligence.CatalogSnapshot{}, fmt.Errorf("read Hyperliquid public dex list: %w", err)
		}
	}
	dexIndex := make(map[string]int, len(dexNames))
	for i, dex := range dexNames {
		if dex != nil {
			dexIndex[dex.Name] = i
		}
	}
	for _, dex := range r.dexes {
		if _, ok := dexIndex[dex]; !ok || dexIndex[dex] == 0 {
			return takeintelligence.CatalogSnapshot{}, fmt.Errorf("configured Hyperliquid dex %q is not listed", dex)
		}
	}

	venues := []string{""}
	venues = append(venues, r.dexes...)
	snapshot := takeintelligence.CatalogSnapshot{
		ID: uuid.NewString(), AdapterVersion: "hyperliquid-public-info-v1", Provider: "hyperliquid",
		ConfiguredVenues: venues, StartedAt: started, Result: takeintelligence.SnapshotComplete,
		Instruments: []takeintelligence.Instrument{},
	}
	annotations := map[string]marketAnnotation{}
	if len(r.dexes) > 0 {
		if err := r.post(ctx, map[string]string{"type": "perpConciseAnnotations"}, &annotationsWire{target: &annotations}); err != nil {
			return takeintelligence.CatalogSnapshot{}, fmt.Errorf("read Hyperliquid public market annotations: %w", err)
		}
	}
	for _, dex := range venues {
		instruments, err := r.fetchDex(ctx, dex, dexIndex, annotations)
		if err != nil {
			return takeintelligence.CatalogSnapshot{}, err
		}
		snapshot.Instruments = append(snapshot.Instruments, instruments...)
	}
	snapshot.CompletedAt = time.Now().UTC()
	if len(snapshot.Instruments) == 0 {
		snapshot.Result = takeintelligence.SnapshotPartial
	}
	digest, err := takeintelligence.CatalogDigest(snapshot)
	if err != nil {
		return takeintelligence.CatalogSnapshot{}, err
	}
	snapshot.Digest = digest
	return snapshot, nil
}

func (r *HyperliquidCatalogReader) fetchDex(ctx context.Context, dex string, dexIndex map[string]int, annotations map[string]marketAnnotation) ([]takeintelligence.Instrument, error) {
	var raw []json.RawMessage
	request := map[string]string{"type": "metaAndAssetCtxs"}
	if dex != "" {
		request["dex"] = dex
	}
	if err := r.post(ctx, request, &raw); err != nil {
		return nil, fmt.Errorf("read Hyperliquid %q metadata and market contexts: %w", dex, err)
	}
	if len(raw) != 2 {
		return nil, fmt.Errorf("Hyperliquid %q metadata response has %d parts", dex, len(raw))
	}
	var meta wireMeta
	var contexts []wireAssetContext
	if err := json.Unmarshal(raw[0], &meta); err != nil {
		return nil, fmt.Errorf("decode Hyperliquid %q metadata: %w", dex, err)
	}
	if err := json.Unmarshal(raw[1], &contexts); err != nil || len(contexts) != len(meta.Universe) {
		return nil, fmt.Errorf("decode Hyperliquid %q market contexts: mismatched asset count", dex)
	}

	capRequest := map[string]string{"type": "perpsAtOpenInterestCap"}
	if dex != "" {
		capRequest["dex"] = dex
	}
	var capped []string
	capKnown := r.post(ctx, capRequest, &capped) == nil
	if !capKnown && dex != "" {
		return nil, fmt.Errorf("read Hyperliquid %q open-interest cap status", dex)
	}
	cappedSet := make(map[string]bool, len(capped))
	for _, coin := range capped {
		cappedSet[coin] = true
	}
	limits := map[string]string{}
	if dex != "" {
		var rawLimits struct {
			CoinToOICap [][2]json.RawMessage `json:"coinToOiCap"`
		}
		if err := r.post(ctx, map[string]string{"type": "perpDexLimits", "dex": dex}, &rawLimits); err != nil {
			return nil, fmt.Errorf("read Hyperliquid %q open-interest limits: %w", dex, err)
		}
		for _, pair := range rawLimits.CoinToOICap {
			var coin string
			if json.Unmarshal(pair[0], &coin) == nil {
				limits[coin] = numberText(pair[1])
			}
		}
	}
	offset := 0
	if dex != "" {
		offset = hip3AssetBase + dexIndex[dex]*hip3DexStride
	}
	observedAt := time.Now().UTC()
	result := make([]takeintelligence.Instrument, 0, len(meta.Universe))
	for i, item := range meta.Universe {
		if item.IsDelisted {
			continue
		}
		ctx := contexts[i]
		annotation := annotations[item.Name]
		marginMode := strings.TrimSpace(item.MarginMode)
		growthKnown := dex == "" || item.GrowthMode == "enabled" || item.GrowthMode == "disabled"
		result = append(result, takeintelligence.Instrument{
			Key:        takeintelligence.ProviderInstrumentKey{Provider: "hyperliquid", Product: "perpetual", Venue: dex, Symbol: item.Name},
			AssetIndex: offset + i, ListingStatus: takeintelligence.ListingObserved,
			MarketType: "perpetual", Category: annotation.Category, DisplayName: fallback(annotation.DisplayName, item.Name), Keywords: slices.Clone(annotation.Keywords),
			MarkPrice: ctx.MarkPrice.String(), OraclePrice: ctx.OraclePrice.String(), FundingRate: ctx.Funding.String(),
			OpenInterest: ctx.OpenInterest.String(), DayNotionalUSD: ctx.DayNotional.String(), ObservedAt: observedAt,
			Liquidity: takeintelligence.LiquiditySnapshot{DayVolumeUSD: ctx.DayNotional.String(), OpenInterestUSD: multiplyDecimalText(ctx.OpenInterest.String(), ctx.MarkPrice.String()), ObservedAt: observedAt},
			AtOICap:   cappedSet[item.Name], AtOICapKnown: capKnown, MaxLeverage: item.MaxLeverage,
			OnlyIsolated: item.OnlyIsolated || (marginMode != "" && marginMode != "normal"), MarginMode: marginMode,
			CollateralToken: fmt.Sprint(meta.CollateralToken), DeployerFeeScale: item.DeployerFeeScale.String(),
			GrowthMode: item.GrowthMode == "enabled", GrowthModeKnown: growthKnown,
			OICapUSD: limits[item.Name],
		})
	}
	return result, nil
}

func (r *HyperliquidCatalogReader) post(ctx context.Context, body any, destination any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("public Hyperliquid API returned HTTP %d", response.StatusCode)
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxHyperliquidBody+1))
	if err != nil {
		return err
	}
	if len(responseBody) > maxHyperliquidBody {
		return errors.New("public Hyperliquid response exceeded the size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("public Hyperliquid response contained multiple JSON values")
		}
		return fmt.Errorf("read trailing public Hyperliquid response: %w", err)
	}
	return nil
}

type wireMeta struct {
	Universe []struct {
		Name             string    `json:"name"`
		MaxLeverage      int       `json:"maxLeverage"`
		OnlyIsolated     bool      `json:"onlyIsolated"`
		IsDelisted       bool      `json:"isDelisted"`
		MarginMode       string    `json:"marginMode"`
		GrowthMode       string    `json:"growthMode"`
		DeployerFeeScale wireValue `json:"deployerFeeScale"`
	} `json:"universe"`
	CollateralToken int `json:"collateralToken"`
}

type wireAssetContext struct {
	Funding      wireValue `json:"funding"`
	OpenInterest wireValue `json:"openInterest"`
	DayNotional  wireValue `json:"dayNtlVlm"`
	MarkPrice    wireValue `json:"markPx"`
	OraclePrice  wireValue `json:"oraclePx"`
}

type wireValue string

func (v *wireValue) UnmarshalJSON(raw []byte) error {
	if bytes.Equal(raw, []byte("null")) {
		*v = ""
		return nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		*v = wireValue(text)
		return nil
	}
	if json.Valid(raw) {
		*v = wireValue(string(raw))
		return nil
	}
	return errors.New("invalid numeric value")
}

func (v wireValue) String() string { return string(v) }

type marketAnnotation struct {
	Category    string   `json:"category"`
	DisplayName string   `json:"displayName"`
	Keywords    []string `json:"keywords"`
}

type annotationsWire struct{ target *map[string]marketAnnotation }

func (wire *annotationsWire) UnmarshalJSON(raw []byte) error {
	var pairs [][2]json.RawMessage
	if err := json.Unmarshal(raw, &pairs); err != nil {
		return err
	}
	result := make(map[string]marketAnnotation, len(pairs))
	for _, pair := range pairs {
		var coin string
		var value marketAnnotation
		if json.Unmarshal(pair[0], &coin) != nil || json.Unmarshal(pair[1], &value) != nil || coin == "" {
			continue
		}
		result[coin] = value
	}
	*wire.target = result
	return nil
}

func numberText(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	if json.Valid(raw) && string(raw) != "null" {
		return string(raw)
	}
	return ""
}

func multiplyDecimalText(left, right string) string {
	if left == "" || right == "" {
		return ""
	}
	a, okA := new(big.Rat).SetString(left)
	b, okB := new(big.Rat).SetString(right)
	if !okA || !okB {
		return ""
	}
	return new(big.Rat).Mul(a, b).FloatString(8)
}

func fallback(value, alternative string) string {
	if strings.TrimSpace(value) == "" {
		return alternative
	}
	return value
}

var _ interface {
	CaptureCatalog(context.Context) (takeintelligence.CatalogSnapshot, error)
} = (*HyperliquidCatalogReader)(nil)
