package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pacenote-sim/plugin"
)

// The half of this plugin the host cannot do for it.
//
// Everything about who may reach which address is the host's: the manifest says
// it and the host enforces it, so there is nothing here to test about that. What
// is left is the signature on the webhook, which is the only thing standing
// between a public address and somebody inventing payments.

// signed is a request as the payment provider would send it.
func signed(t *testing.T, secret string, body []byte) plugin.HTTPRequest {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return plugin.HTTPRequest{
		Method: http.MethodPost,
		Path:   "/webhook",
		Body:   body,
		Header: http.Header{"X-Signature": {hex.EncodeToString(mac.Sum(nil))}},
		Secrets: plugin.Secrets{
			settingSecret: plugin.NewSecret(secret),
		},
	}
}

func TestAPaymentTheProviderReallySent(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	res, err := payments{}.ServeHTTP(t.Context(), signed(t, "the-providers-secret", []byte(`{"paid":true}`)))
	r.NoError(err)
	r.Equal(http.StatusOK, res.Status)
}

// The address is public, so anyone who finds it can post to it. The signature is
// what makes that safe, and these are the ways it is not one.
func TestAPaymentNobodySent(t *testing.T) {
	t.Parallel()

	body := []byte(`{"paid":true}`)
	cases := []struct {
		name string
		req  func(t *testing.T) plugin.HTTPRequest
		want int
	}{
		{
			name: "signed with the wrong secret",
			req: func(t *testing.T) plugin.HTTPRequest {
				t.Helper()
				req := signed(t, "somebody-elses-secret", body)
				req.Secrets = plugin.Secrets{settingSecret: plugin.NewSecret("the-providers-secret")}
				return req
			},
			want: http.StatusUnauthorized,
		},
		{
			name: "not signed at all",
			req: func(t *testing.T) plugin.HTTPRequest {
				t.Helper()
				req := signed(t, "the-providers-secret", body)
				req.Header = nil
				return req
			},
			want: http.StatusUnauthorized,
		},
		{
			// The body changed after it was signed, which is the attack the
			// signature is actually for: a real payment replayed with a bigger
			// number in it.
			name: "a body that changed after it was signed",
			req: func(t *testing.T) plugin.HTTPRequest {
				t.Helper()
				req := signed(t, "the-providers-secret", body)
				req.Body = []byte(`{"paid":true,"amount":999999}`)
				return req
			},
			want: http.StatusUnauthorized,
		},
		{
			// Not configured yet. It answers 503 rather than 200 on purpose: a
			// provider told the payment was taken does not send it again.
			name: "before the operator has configured it",
			req: func(t *testing.T) plugin.HTTPRequest {
				t.Helper()
				req := signed(t, "the-providers-secret", body)
				req.Secrets = nil
				return req
			},
			want: http.StatusServiceUnavailable,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := payments{}.ServeHTTP(t.Context(), tc.req(t))
			require.NoError(t, err)
			require.Equal(t, tc.want, res.Status)
		})
	}
}

// The operator's page says who is reading it, and the address to give the
// provider. Neither is guessed: the host resolved the first from its own
// session table and told this plugin where it is mounted.
func TestTheOperatorsPage(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	res, err := payments{}.ServeHTTP(t.Context(), plugin.HTTPRequest{
		Method:   http.MethodGet,
		Path:     "/",
		Prefix:   "/plugin/payments",
		BaseURL:  "https://pacenote.example.com",
		Caller:   plugin.Caller{AdminEmail: "ana@example.com"},
		Settings: plugin.Values{settingPlan: "Iberian GT season pass"},
	})
	r.NoError(err)
	r.Equal(http.StatusOK, res.Status)
	r.Contains(res.Header.Get("Content-Type"), "text/html")

	page := string(res.Body)
	r.Contains(page, "Iberian GT season pass")
	r.Contains(page, "ana@example.com")
	r.Contains(page, "https://pacenote.example.com/plugin/payments/webhook",
		"the operator was not given the address to hand the provider")
}

// Anything an operator types reaches the page as text and not as markup. It is
// their own server and their own settings, so this is not about an attacker —
// it is about an apostrophe in a league's name not breaking the page.
func TestWhatTheOperatorTypedIsShownAndNotRun(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	res, err := payments{}.ServeHTTP(t.Context(), plugin.HTTPRequest{
		Method:   http.MethodGet,
		Path:     "/",
		Caller:   plugin.Caller{AdminEmail: "ana@example.com"},
		Settings: plugin.Values{settingPlan: `<script>alert(1)</script>`},
	})
	r.NoError(err)
	r.NotContains(string(res.Body), "<script>")
	r.Contains(string(res.Body), "&lt;script&gt;")
}

// The manifest beside this binary is the whole of what this plugin puts on an
// operator's server, so it is worth checking that it says what the code needs.
func TestTheManifestMatchesWhatThisServes(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	raw, err := os.ReadFile("plugin.json")
	r.NoError(err)
	var m plugin.Manifest
	r.NoError(json.Unmarshal(raw, &m))
	r.NoError(m.Validate())

	routes := m.Capabilities.HTTP
	r.NotNil(routes, "the manifest asks for no routes and this plugin serves two")

	// The webhook has to be reachable by a machine with no session.
	access, ok := routes.For("/webhook")
	r.True(ok)
	r.Equal(plugin.AccessPublic, access)

	// And everything else is the operator's.
	access, ok = routes.For("/")
	r.True(ok)
	r.Equal(plugin.AccessAdmin, access)
	access, ok = routes.For("/refunds")
	r.True(ok)
	r.Equal(plugin.AccessAdmin, access, "a page beside the others was left public")
}

// The page before the operator has named a plan. It says something rather than
// rendering an empty heading.
func TestTheOperatorsPageBeforeTheyNamedAPlan(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	res, err := payments{}.ServeHTTP(t.Context(), plugin.HTTPRequest{
		Method: http.MethodGet,
		Path:   "/",
		Caller: plugin.Caller{AdminEmail: "ana@example.com"},
	})
	r.NoError(err)
	r.Equal(http.StatusOK, res.Status)
	r.Contains(string(res.Body), "Season pass")
}

// The three calls this plugin answers because the interface has them, not
// because it does anything with them. A plugin that serves pages still has to
// be a plugin.
func TestWhatThisPluginDoesWithTheRestOfTheInterface(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	settings, err := payments{}.Settings(t.Context())
	r.NoError(err)
	r.NoError(plugin.ValidateSettings(settings), "this plugin declares settings the host would refuse")
	r.Equal([]string{settingSecret, settingPlan}, []string{settings[0].Name, settings[1].Name})

	use, err := payments{}.Notify(t.Context(), plugin.Event{Kind: plugin.EventLapCompleted})
	r.NoError(err)
	r.Zero(use.Total(), "a plugin that spends nothing reported a cost")

	// It is not a plugin anybody asks for a sentence, and it says so rather
	// than answering with an empty one.
	_, err = payments{}.Answer(t.Context(), plugin.Request{ID: "r1", Kind: plugin.RequestSpeak, Text: "x"})
	r.ErrorIs(err, plugin.ErrNoAnswer)
}
