package sdkerr

import "testing"

func TestNew(t *testing.T) {
	cases := []struct {
		key     Key
		context string
		code    int
		message string
	}{
		{UserRejected, "", 5000, "User rejected."},
		{MissingOrInvalid, "request() method", 5, "Missing or invalid. request() method"},
		{UnauthorizedUpdateRequest, "", 3003, "Unauthorized update request."},
		{NoMatchingKey, "session topic doesn't exist: abc", 2, "No matching key. session topic doesn't exist: abc"},
		{Key("NOT_A_KEY"), "", 5, "Missing or invalid."},
	}
	for _, c := range cases {
		e := New(c.key, c.context)
		if e.Code != c.code || e.Message != c.message {
			t.Errorf("%s: got %d %q, want %d %q", c.key, e.Code, e.Message, c.code, c.message)
		}
	}
}
