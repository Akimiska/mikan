// Package model is the panel's data as plain values, without the store, and the rules and
// texts computed from it. The bot addon (tgbot) is built on it alone, so it does not link the
// panel; billing and domain compute the same rules with it, converting their rows
// (model.User(u) for a db.User). What crosses the bot's RPC is held to golden files
// (testdata): a change of the panel's tables does not change what an installed bot reads.
package model

import "database/sql"

// Each is in with every element through f, as the panel's values convert to the model's; nil
// stays nil, so the JSON keeps its null.
func Each[A, B any](in []A, f func(A) B) []B {
	if in == nil {
		return nil
	}
	out := make([]B, len(in))
	for i, a := range in {
		out[i] = f(a)
	}
	return out
}

// The rows below are the store's (internal/panel/store/db) field for field, so a row
// converts to them as it is. Their JSON is what the bot's RPC has always carried: the fields
// by their names, a nullable one as {"Int64":…,"Valid":…}. A column added to a table does not
// reach the bot until it is added here, and a column renamed there keeps its name here.

// User is a subscription.
type User struct {
	ID                int64          `json:"ID"`
	Name              string         `json:"Name"`
	Contact           string         `json:"Contact"`
	Note              string         `json:"Note"`
	Tags              string         `json:"Tags"`
	Status            string         `json:"Status"`
	TariffID          sql.NullInt64  `json:"TariffID"`
	TrafficLimit      sql.NullInt64  `json:"TrafficLimit"`
	DeviceLimit       sql.NullInt64  `json:"DeviceLimit"`
	ResetStrategy     string         `json:"ResetStrategy"`
	PeriodDays        int64          `json:"PeriodDays"`
	PeriodStart       int64          `json:"PeriodStart"`
	UsedUp            int64          `json:"UsedUp"`
	UsedDown          int64          `json:"UsedDown"`
	TotalUp           int64          `json:"TotalUp"`
	TotalDown         int64          `json:"TotalDown"`
	ExpiresAt         sql.NullInt64  `json:"ExpiresAt"`
	Inbounds          sql.NullString `json:"Inbounds"`
	SubToken          string         `json:"SubToken"`
	SlotID            sql.NullInt64  `json:"SlotID"`
	OnlineAt          sql.NullInt64  `json:"OnlineAt"`
	CreatedAt         int64          `json:"CreatedAt"`
	UpdatedAt         int64          `json:"UpdatedAt"`
	BillingDay        sql.NullInt64  `json:"BillingDay"`
	Source            string         `json:"Source"`
	Hidden            int64          `json:"Hidden"`
	FolderID          sql.NullInt64  `json:"FolderID"`
	SpeedLimit        sql.NullInt64  `json:"SpeedLimit"`
	DeviceMode        string         `json:"DeviceMode"`
	ExtraDevices      int64          `json:"ExtraDevices"`
	ExtraDevicesUntil sql.NullInt64  `json:"ExtraDevicesUntil"`
}

// TgChat is a Telegram account's chat with the bot.
type TgChat struct {
	TgID       int64         `json:"TgID"`
	Username   string        `json:"Username"`
	FirstName  string        `json:"FirstName"`
	MenuMsgID  int64         `json:"MenuMsgID"`
	Current    int64         `json:"Current"`
	Blocked    int64         `json:"Blocked"`
	CreatedAt  int64         `json:"CreatedAt"`
	UpdatedAt  int64         `json:"UpdatedAt"`
	CampaignID sql.NullInt64 `json:"CampaignID"`
	CampaignAt sql.NullInt64 `json:"CampaignAt"`
}

// BoundDevice is a device bound to a subscription.
type BoundDevice struct {
	ID        int64  `json:"ID"`
	UserID    int64  `json:"UserID"`
	Hwid      string `json:"Hwid"`
	SlotID    int64  `json:"SlotID"`
	Os        string `json:"Os"`
	OsVersion string `json:"OsVersion"`
	Model     string `json:"Model"`
	App       string `json:"App"`
	LastIp    string `json:"LastIp"`
	CreatedAt int64  `json:"CreatedAt"`
	LastSeen  int64  `json:"LastSeen"`
	Name      string `json:"Name"`
}

// Tariff is a tariff.
type Tariff struct {
	ID               int64         `json:"ID"`
	Name             string        `json:"Name"`
	TrafficLimit     sql.NullInt64 `json:"TrafficLimit"`
	DurationDays     int64         `json:"DurationDays"`
	DeviceLimit      sql.NullInt64 `json:"DeviceLimit"`
	ResetStrategy    string        `json:"ResetStrategy"`
	PriceLabel       string        `json:"PriceLabel"`
	Sort             int64         `json:"Sort"`
	Archived         int64         `json:"Archived"`
	CreatedAt        int64         `json:"CreatedAt"`
	BillingDay       sql.NullInt64 `json:"BillingDay"`
	PriceStars       sql.NullInt64 `json:"PriceStars"`
	PriceRub         sql.NullInt64 `json:"PriceRub"`
	OnSale           int64         `json:"OnSale"`
	SpeedLimit       sql.NullInt64 `json:"SpeedLimit"`
	Description      string        `json:"Description"`
	DeviceMode       string        `json:"DeviceMode"`
	DevicePriceStars sql.NullInt64 `json:"DevicePriceStars"`
	DevicePriceRub   sql.NullInt64 `json:"DevicePriceRub"`
	DeviceExtraMax   int64         `json:"DeviceExtraMax"`
	ChosenServers    int64         `json:"ChosenServers"`
}

// Payment is a payment.
type Payment struct {
	ID         int64          `json:"ID"`
	Provider   string         `json:"Provider"`
	Payload    string         `json:"Payload"`
	ExternalID sql.NullString `json:"ExternalID"`
	TgID       int64          `json:"TgID"`
	Kind       string         `json:"Kind"`
	UserID     sql.NullInt64  `json:"UserID"`
	TariffID   sql.NullInt64  `json:"TariffID"`
	PackageID  sql.NullInt64  `json:"PackageID"`
	TariffName string         `json:"TariffName"`
	Amount     int64          `json:"Amount"`
	Currency   string         `json:"Currency"`
	Status     string         `json:"Status"`
	Error      string         `json:"Error"`
	PayUrl     string         `json:"PayUrl"`
	CreatedAt  int64          `json:"CreatedAt"`
	PaidAt     sql.NullInt64  `json:"PaidAt"`
	AppliedAt  sql.NullInt64  `json:"AppliedAt"`
	RefundedAt sql.NullInt64  `json:"RefundedAt"`
	TermDays   sql.NullInt64  `json:"TermDays"`
	Revert     string         `json:"Revert"`
}

// Topup is a request to top up an account's balance by hand.
type Topup struct {
	ID        int64         `json:"ID"`
	TgID      int64         `json:"TgID"`
	Amount    int64         `json:"Amount"`
	Currency  string        `json:"Currency"`
	Photo     string        `json:"Photo"`
	Status    string        `json:"Status"`
	CreatedAt int64         `json:"CreatedAt"`
	DecidedAt sql.NullInt64 `json:"DecidedAt"`
	AdminID   sql.NullInt64 `json:"AdminID"`
	Reason    string        `json:"Reason"`
}

// BalanceOp is one change of an account's balance.
type BalanceOp struct {
	ID        int64         `json:"ID"`
	TgID      int64         `json:"TgID"`
	Amount    int64         `json:"Amount"`
	Kind      string        `json:"Kind"`
	PaymentID sql.NullInt64 `json:"PaymentID"`
	TopupID   sql.NullInt64 `json:"TopupID"`
	AdminID   sql.NullInt64 `json:"AdminID"`
	Note      string        `json:"Note"`
	CreatedAt int64         `json:"CreatedAt"`
}

// TrafficPackage is a traffic package of the catalog.
type TrafficPackage struct {
	ID         int64         `json:"ID"`
	Name       string        `json:"Name"`
	Bytes      int64         `json:"Bytes"`
	PoolID     sql.NullInt64 `json:"PoolID"`
	Lifetime   string        `json:"Lifetime"`
	Days       int64         `json:"Days"`
	PriceStars sql.NullInt64 `json:"PriceStars"`
	PriceRub   sql.NullInt64 `json:"PriceRub"`
	OnSale     int64         `json:"OnSale"`
	Sort       int64         `json:"Sort"`
	Archived   int64         `json:"Archived"`
	CreatedAt  int64         `json:"CreatedAt"`
}
