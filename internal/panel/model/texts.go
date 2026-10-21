package model

import (
	"fmt"
	"strings"

	"mikan/internal/textutil"
)

// What a purchase is called in invoices, the bot and the Mini App: the term and the limits of
// a tariff, its groups and gifts, a package and one more device. The Describe functions fill
// in the texts of one language (textsFor); none branches on the language.

type texts struct {
	// A term: none, a month or so many of them, so many days.
	noEnd, oneMonth, months, days string
	// The limits: "%s GB" takes gigabytes; decimal is the point of a part of a gigabyte.
	gigabytes, decimal, unlimitedTraffic, devicesAtOnce, devices, speed string
	// A server group of a tariff (its name first), and a gift with every payment.
	groupMain, groupOwn, groupUnlimited, groupClosed, gift string
	// A package: where it adds, and how long it lasts.
	mainTraffic, untilPeriodEnds, forDays, untilUsedUp string
	// One more device: the purchase, and what it gives with renewals or until the term ends.
	device, deviceKept, deviceTerm string
}

var ruTexts = texts{
	noEnd:            "бессрочно",
	oneMonth:         "1 мес.",
	months:           "%d мес.",
	days:             "%d дн.",
	gigabytes:        "%s ГБ",
	decimal:          ",",
	unlimitedTraffic: "трафик без лимита",
	devicesAtOnce:    "устройств одновременно: %d",
	devices:          "устройств: %d",
	speed:            "до %d Мбит/с",
	groupMain:        "%s: входит",
	groupOwn:         "%s: %s ГБ отдельно",
	groupUnlimited:   "%s: без лимита",
	groupClosed:      "%s: не входит",
	gift:             "В подарок: %s",
	mainTraffic:      "основной трафик",
	untilPeriodEnds:  "до конца периода",
	forDays:          "на %d дн.",
	untilUsedUp:      "пока не израсходован",
	device:           "+1 устройство",
	deviceKept:       "Ещё одно устройство к подписке: всего %d. Остаётся при продлении.",
	deviceTerm:       "Ещё одно устройство к подписке: всего %d, до конца срока.",
}

var enTexts = texts{
	noEnd:            "no end date",
	oneMonth:         "1 month",
	months:           "%d months",
	days:             "%d days",
	gigabytes:        "%s GB",
	decimal:          ".",
	unlimitedTraffic: "unlimited traffic",
	devicesAtOnce:    "devices at once: %d",
	devices:          "devices: %d",
	speed:            "up to %d Mbit/s",
	groupMain:        "%s: included",
	groupOwn:         "%s: %s GB of its own",
	groupUnlimited:   "%s: unlimited",
	groupClosed:      "%s: not included",
	gift:             "Gift: %s",
	mainTraffic:      "main traffic",
	untilPeriodEnds:  "until the period ends",
	forDays:          "for %d days",
	untilUsedUp:      "until used up",
	device:           "+1 device",
	deviceKept:       "One more device for the subscription: %d in all. It stays with renewals.",
	deviceTerm:       "One more device for the subscription: %d in all, until the term ends.",
}

// textsFor is the texts of lang (textutil.Lang: English for "en", Russian for anything else).
func textsFor(lang string) *texts { return textutil.ByLang(lang, &ruTexts, &enTexts) }

// gb is n bytes in gigabytes: "50", or "1.5" ("1,5" in Russian) for a part of a GB.
func (w *texts) gb(n int64) string {
	if n%GiB == 0 {
		return fmt.Sprint(n / GiB)
	}
	return strings.Replace(strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/float64(GiB)), ".0"), ".", w.decimal, 1)
}

// Describe is tariff t bought for a term of days in a line, "30 days · 100 GB · 3 devices",
// in lang ("en", else Russian): invoices, the bot and the Mini App show it.
func Describe(t Tariff, days int64, lang string) string {
	return TermLabel(t, days, lang) + " · " + DescribeLimits(t, lang)
}

// TermLabel is a term of tariff t in a few words: "30 days", "3 months" for a tariff that
// ends on a billing day, "no end date".
func TermLabel(t Tariff, days int64, lang string) string {
	w := textsFor(lang)
	switch {
	case days <= 0:
		return w.noEnd
	case t.BillingDay.Valid:
		n := TermMonths(days)
		if n == 1 {
			return w.oneMonth
		}
		return fmt.Sprintf(w.months, n)
	}
	return fmt.Sprintf(w.days, days)
}

// DescribeOffer is a tariff on sale in a line: Describe with its one term, or the range of
// its terms, "7 days – 90 days · 100 GB · 3 devices".
func DescribeOffer(o Offer, lang string) string {
	switch len(o.Terms) {
	case 0:
		return Describe(o.Tariff, o.Tariff.DurationDays, lang)
	case 1:
		return Describe(o.Tariff, o.Terms[0].Days, lang)
	}
	var lo, hi int64 = -1, -1
	forever := false
	for _, t := range o.Terms {
		if t.Days <= 0 {
			forever = true
			continue
		}
		if lo < 0 || t.Days < lo {
			lo = t.Days
		}
		hi = max(hi, t.Days)
	}
	if forever {
		hi = 0
	}
	return TermLabel(o.Tariff, lo, lang) + " – " + TermLabel(o.Tariff, hi, lang) + " · " + DescribeLimits(o.Tariff, lang)
}

// DescribeLimits is what tariff t gives whatever the term: "100 GB · 3 devices".
func DescribeLimits(t Tariff, lang string) string {
	w := textsFor(lang)
	parts := []string{}
	if t.TrafficLimit.Valid {
		// 1,5 GB as it is, not as 1: the admin's form takes parts of a gigabyte.
		parts = append(parts, fmt.Sprintf(w.gigabytes, w.gb(t.TrafficLimit.Int64)))
	} else {
		parts = append(parts, w.unlimitedTraffic)
	}
	if t.DeviceLimit.Valid && t.DeviceMode == DeviceModeOnline {
		// The buyer is not told of places: any number of devices, so many online at once.
		parts = append(parts, fmt.Sprintf(w.devicesAtOnce, t.DeviceLimit.Int64))
	} else if t.DeviceLimit.Valid {
		parts = append(parts, fmt.Sprintf(w.devices, t.DeviceLimit.Int64))
	}
	if t.SpeedLimit.Valid {
		parts = append(parts, fmt.Sprintf(w.speed, t.SpeedLimit.Int64))
	}
	return strings.Join(parts, " · ")
}

// DescribeContents is what offer o is made of besides its limits, a line each, in lang ("en",
// else Russian): its server groups ("WL: 50 GB of its own", "Premium: not included") and its
// gifts ("Gift: 50 GB · WL · until used up"). None for a tariff without groups or gifts.
func DescribeContents(o Offer, lang string) []string {
	w := textsFor(lang)
	var out []string
	for _, g := range o.Groups {
		switch g.Mode {
		case PoolMain:
			out = append(out, fmt.Sprintf(w.groupMain, g.Name))
		case PoolOwn:
			out = append(out, fmt.Sprintf(w.groupOwn, g.Name, w.gb(g.Limit)))
		case PoolUnlimited:
			out = append(out, fmt.Sprintf(w.groupUnlimited, g.Name))
		case PoolClosed:
			out = append(out, fmt.Sprintf(w.groupClosed, g.Name))
		}
	}
	for _, g := range o.Gifts {
		out = append(out, fmt.Sprintf(w.gift, DescribePackage(g.Package, g.Pool, lang)))
	}
	return out
}

// DescribePackage is a package in a line, "50 GB · WL · until the period ends", in lang ("en",
// else Russian); pool is the pool's name, "" for the main traffic.
func DescribePackage(p TrafficPackage, pool, lang string) string {
	w := textsFor(lang)
	size := fmt.Sprintf(w.gigabytes, w.gb(p.Bytes))
	target := pool
	if target == "" {
		target = w.mainTraffic
	}
	var term string
	switch p.Lifetime {
	case LifetimePeriod:
		term = w.untilPeriodEnds
	case LifetimeDays:
		term = fmt.Sprintf(w.forDays, p.Days)
	default:
		term = w.untilUsedUp
	}
	return strings.Join([]string{size, target, term}, " · ")
}

// DescribeDevice names the purchase of one more device: "+1 device".
func DescribeDevice(lang string) string { return textsFor(lang).device }

// DescribeDeviceOffer says what one more device gives and for how long.
func DescribeDeviceOffer(o DeviceOffer, lang string) string {
	w := textsFor(lang)
	if o.Keep {
		return fmt.Sprintf(w.deviceKept, o.Limit+1)
	}
	return fmt.Sprintf(w.deviceTerm, o.Limit+1)
}
