package billing

import "mikan/internal/panel/model"

// The shop as the bot addon sees it, and as its rules and texts are computed (package model):
// billing's types convert to the model's field for field.

// Model is the setup as the model has it.
func (c Config) Model() model.Config {
	return model.Config{Enabled: c.Enabled, Stars: c.Stars, AllowNew: c.AllowNew, RenewResetsTraffic: c.RenewResetsTraffic,
		TrialTariffID: c.TrialTariffID, TrialStars: c.TrialStars, TrialRub: c.TrialRub, FreeTariffID: c.FreeTariffID,
		FreeCondition: c.FreeCondition, ExtraDevicesKeep: c.ExtraDevicesKeep, Balance: model.BalanceConfig(c.Balance)}
}

// Model is the providers as the model has them.
func (a Available) Model() model.Available {
	return model.Available{Stars: a.Stars, Addons: a.Addons, Balance: model.BalanceConfig(a.Balance)}
}

// Model is the offer as the model has it.
func (o Offer) Model() model.Offer {
	return model.Offer{Tariff: model.Tariff(o.Tariff), First: o.First, Stars: o.Stars, Rub: o.Rub,
		Terms:  model.Each(o.Terms, func(t OfferTerm) model.OfferTerm { return model.OfferTerm(t) }),
		Groups: model.Each(o.Groups, func(g OfferGroup) model.OfferGroup { return model.OfferGroup(g) }),
		Gifts: model.Each(o.Gifts, func(g OfferGift) model.OfferGift {
			return model.OfferGift{Package: model.TrafficPackage(g.Package), Pool: g.Pool}
		})}
}

// Model is the package offer as the model has it.
func (o PackageOffer) Model() model.PackageOffer {
	return model.PackageOffer{Package: model.TrafficPackage(o.Package), Pool: o.Pool, Stars: o.Stars, Rub: o.Rub}
}

// Model is the device offer as the model has it.
func (o DeviceOffer) Model() model.DeviceOffer { return model.DeviceOffer(o) }

// Model is the trial offer as the model has it.
func (o TrialOffer) Model() model.TrialOffer {
	return model.TrialOffer{Tariff: model.Tariff(o.Tariff), Stars: o.Stars, Rub: o.Rub}
}

// Model is the free plan's offer as the model has it.
func (o FreeOffer) Model() model.FreeOffer {
	return model.FreeOffer{Tariff: model.Tariff(o.Tariff), Channel: o.Channel}
}
