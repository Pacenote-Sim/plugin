package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

	// And it says who it asks, so the operator reads it before installing.
	r.Equal([]string{"drivers"}, m.Capabilities.Asks)
	r.True(m.Capabilities.MayAsk("drivers"))
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

// The two calls this plugin answers because the interface has them, not because
// it does anything with them. A plugin that serves pages still has to be a
// plugin — and it does not answer anybody, so it is not an Answerer at all.
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

	var p any = payments{}
	_, answers := p.(plugin.Answerer)
	r.False(answers, "nobody asks this plugin anything, and it does not pretend otherwise")
	_, asks := p.(plugin.Asker)
	r.True(asks, "it asks the drivers plugin, so it has to be connectable")
}

// fakeHost stands in for the server: it answers drivers.lookup however the test
// says, and remembers what it was asked.
type fakeHost struct {
	answer func(kind string, payload json.RawMessage) (json.RawMessage, error)
	asked  []string
}

func (h *fakeHost) Ask(_ context.Context, kind string, payload json.RawMessage) (json.RawMessage, error) {
	h.asked = append(h.asked, kind)
	return h.answer(kind, payload)
}

// The question this plugin asks, and what it does with each answer. These are
// not parallel: the host is a package variable, handed over once at start.
func TestAPaymentIsCheckedAgainstTheDriversPlugin(t *testing.T) { //nolint:paralleltest // the connected host is process-wide, as it is in a real plugin.
	body := []byte(`{"paid":true,"slug":"ana-ruiz"}`)

	cases := []struct {
		name   string
		answer func(kind string, payload json.RawMessage) (json.RawMessage, error)
		want   int
		text   string
	}{
		{
			name: "a driver the team knows is taken",
			answer: func(_ string, payload json.RawMessage) (json.RawMessage, error) {
				require.JSONEq(t, `{"slug":"ana-ruiz"}`, string(payload), "the question is the slug the payment named")
				return json.RawMessage(`{"known":true,"name":"Ana Ruiz"}`), nil
			},
			want: http.StatusOK, text: "taken",
		},
		{
			name:   "a driver nobody knows is ignored, and the provider is told so",
			answer: func(string, json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`{"known":false}`), nil },
			want:   http.StatusOK, text: "ignored: no driver called ana-ruiz",
		},
		{
			name:   "no drivers plugin to ask: taken, and said to be unchecked",
			answer: func(string, json.RawMessage) (json.RawMessage, error) { return nil, plugin.ErrUnavailable },
			want:   http.StatusOK, text: "unchecked",
		},
		{
			name:   "a drivers plugin that fell over: the provider is asked to try again",
			answer: func(string, json.RawMessage) (json.RawMessage, error) { return nil, errors.New("it fell over") },
			want:   http.StatusServiceUnavailable, text: "did not answer",
		},
	}
	for _, tc := range cases { //nolint:paralleltest // the connected host is process-wide, as it is in a real plugin.
		t.Run(tc.name, func(t *testing.T) {
			r := require.New(t)
			host := &fakeHost{answer: tc.answer}
			payments{}.Connected(host)

			res, err := payments{}.ServeHTTP(t.Context(), signed(t, "the-providers-secret", body))
			r.NoError(err)
			r.Equal(tc.want, res.Status, string(res.Body))
			r.Contains(string(res.Body), tc.text)
			r.Equal([]string{"drivers.lookup"}, host.asked, "one question, to the plugin whose name is in it")
		})
	}

	t.Run("a payment that names no driver is not checked", func(t *testing.T) {
		r := require.New(t)
		host := &fakeHost{answer: func(string, json.RawMessage) (json.RawMessage, error) {
			r.Fail("nothing to ask about")
			return nil, nil
		}}
		payments{}.Connected(host)

		res, err := payments{}.ServeHTTP(t.Context(), signed(t, "the-providers-secret", []byte(`{"paid":true}`)))
		r.NoError(err)
		r.Equal(http.StatusOK, res.Status)
		r.Empty(host.asked)
	})
}
