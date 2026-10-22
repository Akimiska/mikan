package panelapi

// AoiVPN fork: the referral programme's calls of the bot (internal/panel/aoiref), open to
// the bot addon like the others: about the account that is talking to the bot only.
func init() {
	botRPC["Referral"] = rule("users:read", []int{0}, nil)
	botRPC["Refer"] = rule("users:write", []int{0}, nil)
}
