package billing

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"mikan/internal/panel/store/db"
)

// cryptoBot is the Crypto Pay API (@CryptoBot): the token goes in Crypto-Pay-API-Token.
// Invoices are in rubles; the payer picks the coin.
type cryptoBot struct {
	base, token string
	hc          *http.Client
}

type cbInvoice struct {
	ID         int64      `json:"invoice_id"`
	Status     string     `json:"status"` // active | paid | expired
	Fiat       string     `json:"fiat"`
	Amount     flexNumber `json:"amount"` // a string in the docs, a number in some answers
	Payload    string     `json:"payload"`
	BotURL     string     `json:"bot_invoice_url"`
	MiniAppURL string     `json:"mini_app_invoice_url"`
}

func (c cryptoBot) call(ctx context.Context, method string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/"+method, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Crypto-Pay-API-Token", c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  struct {
			Name string `json:"name"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&env); err != nil {
		return &providerError{Code: "cryptobot_bad_response", Status: resp.StatusCode}
	}
	if !env.OK {
		name := env.Error.Name
		if name == "" {
			name = "error"
		}
		return &providerError{Code: "cryptobot_" + name, Status: resp.StatusCode}
	}
	return json.Unmarshal(env.Result, out)
}

func (c cryptoBot) create(ctx context.Context, payload string, kopecks int64, description, backURL string) (id, url string, err error) {
	body := map[string]any{
		"currency_type": "fiat",
		"fiat":          "RUB",
		"amount":        rubles(kopecks),
		"description":   truncate(description, 1024),
		"payload":       payload,
		"expires_in":    int(cryptoInvoiceTTL.Seconds()),
	}
	if backURL != "" {
		body["paid_btn_name"], body["paid_btn_url"] = "openBot", backURL
	}
	var inv cbInvoice
	if err := c.call(ctx, "createInvoice", body, &inv); err != nil {
		return "", "", err
	}
	if inv.ID == 0 || inv.BotURL == "" {
		return "", "", &providerError{Code: "cryptobot_bad_response", Status: 200}
	}
	return strconv.FormatInt(inv.ID, 10), inv.BotURL, nil
}

func (c cryptoBot) get(ctx context.Context, id string) (cbInvoice, error) {
	var res struct {
		Items []cbInvoice `json:"items"`
	}
	if err := c.call(ctx, "getInvoices", map[string]any{"invoice_ids": id}, &res); err != nil {
		return cbInvoice{}, err
	}
	for _, inv := range res.Items {
		if strconv.FormatInt(inv.ID, 10) == id {
			return inv, nil
		}
	}
	return cbInvoice{}, &providerError{Code: "cryptobot_not_found", Status: 200}
}

// validCryptoSignature checks crypto-pay-api-signature: HMAC-SHA256 of the raw body with
// SHA-256 of the app token as the key, compared in constant time.
func validCryptoSignature(token string, body []byte, signature string) bool {
	if token == "" || signature == "" {
		return false
	}
	got, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	key := sha256.Sum256([]byte(token))
	m := hmac.New(sha256.New, key[:])
	m.Write(body)
	return hmac.Equal(got, m.Sum(nil))
}

// checkCryptoBot asks CryptoBot about an invoice and records what it says.
func (s *Service) checkCryptoBot(ctx context.Context, id string) error {
	cb := s.cryptoBot(ctx)
	if cb.token == "" {
		return ErrProviderOff
	}
	pay, err := s.d.Store.Q.GetPaymentByExternal(ctx, db.GetPaymentByExternalParams{Provider: CryptoBot, ExternalID: sql.NullString{String: id, Valid: true}})
	if err != nil {
		return ErrBadPayment
	}
	inv, err := cb.get(ctx, id)
	if err != nil {
		return err
	}
	switch {
	case inv.Payload != pay.Payload:
		s.d.Log.Error("billing: cryptobot invoice does not match", "payment", pay.ID)
		return ErrBadPayment
	case inv.Status == "paid":
		if inv.Fiat != pay.Currency || kopecks(string(inv.Amount)) != pay.Amount {
			s.d.Log.Error("billing: cryptobot amount differs", "payment", pay.ID, "got", inv.Amount, "fiat", inv.Fiat)
			return ErrBadPayment
		}
		return s.paid(ctx, pay, id)
	case inv.Status == "expired":
		_, err := s.d.Store.Q.SetPaymentStatus(ctx, db.SetPaymentStatusParams{NewStatus: "expired", ID: pay.ID, OldStatus: "pending"})
		return err
	}
	return nil
}

// cryptoUpdate is a webhook update; only its invoice id is used, the rest is asked again.
type cryptoUpdate struct {
	Type    string    `json:"update_type"`
	Payload cbInvoice `json:"payload"`
}

// flexNumber takes a JSON string or number: "199.00" and 199 both mean the same sum.
type flexNumber string

func (f *flexNumber) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = flexNumber(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*f = flexNumber(n.String())
	return nil
}
