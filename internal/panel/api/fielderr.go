package api

import "github.com/danielgtaylor/huma/v2"

// A refused input is a 422 "validation" whose details name each refused field (location,
// such as "body.name") and the code the UI turns into words (errors.api.<code> in
// web/src/i18n, see ApiError in web/src/api/client.ts). Handlers build them here and
// nowhere else, so the shape the UI reads stays one. The few that keep a top-level code of
// their own are listed in direct422 (fielderr_test.go), which fails on any other.

// fieldErr refuses the field at location with code.
func fieldErr(location, code string) error {
	return huma.Error422UnprocessableEntity("validation", &huma.ErrorDetail{Location: location, Message: code})
}

// fieldErrValue is fieldErr with the value the text carries: the line of a bad rule, the
// port that is busy.
func fieldErrValue(location, code string, value any) error {
	return huma.Error422UnprocessableEntity("validation", &huma.ErrorDetail{Location: location, Message: code, Value: value})
}

// fieldErrs refuses several fields at once; the details are *huma.ErrorDetail each.
func fieldErrs(details ...error) error {
	return huma.Error422UnprocessableEntity("validation", details...)
}

// refusal is a 422 with no field to name, because what is refused is the state or the whole
// request (a node that cannot take the change, a scan with no address to scan): the code
// alone, which the UI reads from the top-level detail.
func refusal(code string) error {
	return huma.Error422UnprocessableEntity(code)
}

// fieldDetail is one refused field among several, for fieldErrs and for checks that
// collect everything wrong before answering; value may be nil.
func fieldDetail(location, code string, value any) *huma.ErrorDetail {
	return &huma.ErrorDetail{Location: location, Message: code, Value: value}
}
