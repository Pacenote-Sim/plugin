// payments is an example plugin that serves two addresses and needs a different
// answer at each: a webhook a payment provider posts to, and pages only the
// operator may open.
//
// It is here to show the shape rather than to take anybody's money. Nothing
// below speaks to a real provider; the signature check is real because that is
// the part the host cannot do, and the rest is a stub.
//
// The thing worth noticing is what is *not* here. There is no session handling,
// no cookie, no check that the caller is an administrator. The manifest says
// which addresses this plugin serves and who may reach each, the host enforces
// it before anything is forwarded, and an address the manifest does not list
// never arrives at all. What is left is the part only this plugin can do.
//
// It also asks another plugin something. A payment names a driver, and whether
// that driver is one of the team's is the drivers plugin's to say — so this one
// asks it, through the server, and its manifest says so: "asks": ["drivers"].
package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"
	"sync"

	"github.com/pacenote-sim/plugin"
)

// The operator's settings. The signing secret is the provider's, and it is the
// one thing here that must not be readable anywhere: it is declared as a secret
// so the host seals it and hands it over one call at a time.
const (
	settingSecret = "webhook_secret"
	settingPlan   = "plan_name"
)

func main() { plugin.Serve(payments{}) }

type payments struct{}

// The host this plugin asks through, from Connected. It is a package variable
// because payments is a value and the host arrives once, at start.
var (
	hostMu  sync.Mutex
	theHost plugin.Host
)

// Connected keeps the host. The manifest declares "asks": ["drivers"], which is
// why the server offers one.
func (p payments) Connected(h plugin.Host) {
	hostMu.Lock()
	defer hostMu.Unlock()
	theHost = h
}

func (p payments) Settings(context.Context) ([]plugin.Setting, error) {
	return []plugin.Setting{
		{
			Name:  settingSecret,
			Label: "Webhook signing secret",
			Help: "The secret your payment provider shows you when you add the webhook. " +
				"It is what proves a payment really came from them.",
			Kind:     plugin.KindSecret,
			Required: true,
		},
		{
			Name:    settingPlan,
			Label:   "What the subscription is called",
			Help:    "Shown to your drivers on the receipt.",
			Kind:    plugin.KindText,
			Default: "Season pass",
		},
	}, nil
}

// This plugin wants no events and answers nobody. It exists to serve two
// addresses and to ask one question, which is a whole plugin on its own.
func (p payments) Notify(context.Context, plugin.Event) (plugin.Usage, error) {
	return plugin.Usage{}, nil
}

// ServeHTTP answers both addresses.
//
// There is no authorisation here and there does not need to be. By the time a
// request arrives the host has already decided that whoever made it may reach
// the address they asked for: the webhook is public because a provider's server
// has no session, and everything else is the operator's because the manifest
// says so. A path neither route covers never gets this far.
func (p payments) ServeHTTP(ctx context.Context, r plugin.HTTPRequest) (plugin.HTTPResponse, error) {
	if strings.HasPrefix(r.Path, "/webhook") {
		return p.webhook(ctx, r)
	}
	return p.page(ctx, r)
}

// webhook takes one payment from the provider.
//
// The host let this through because the manifest says anyone may reach it, and
// anyone includes whoever finds the address. So the first thing this does is
// the only thing that makes the address safe: check that the body really came
// from the provider, using the secret the operator gave the host.
//
// The comparison is constant-time. A signature check that returns early on the
// first wrong byte tells an attacker how much of their guess was right.
func (p payments) webhook(ctx context.Context, r plugin.HTTPRequest) (plugin.HTTPResponse, error) {
	secret, ok := r.Secrets.Get(settingSecret)
	if !ok {
		// Not configured. Answering 503 rather than 200 matters: a provider
		// that is told a payment was taken will not send it again.
		return plugin.Text(http.StatusServiceUnavailable,
			"This server is not set up to take payments yet."), nil
	}

	want := signature(secret.Value(), r.Body)
	got := r.Header.Get("X-Signature")
	if !hmac.Equal([]byte(want), []byte(got)) {
		return plugin.Text(http.StatusUnauthorized, "That is not signed by the payment provider."), nil
	}

	// The payment names a driver, and whether they are one of the team's is
	// not this plugin's to know: the drivers plugin knows. Ask it.
	var payment struct {
		Slug string `json:"slug"`
	}
	_ = json.Unmarshal(r.Body, &payment)
	if payment.Slug != "" {
		known, err := p.driverKnown(ctx, payment.Slug)
		switch {
		case errors.Is(err, plugin.ErrUnavailable), errors.Is(err, plugin.ErrNotAllowed):
			// No drivers plugin to ask, or this one was not allowed to. The
			// payment is real either way; say plainly that it was not checked.
			return plugin.Text(http.StatusOK, "taken, unchecked: no drivers plugin answered"), nil
		case err != nil:
			// The provider will send it again, which is what 503 asks for. The
			// error is answered, not returned: a webhook that errors is a
			// webhook the host answers 500 for, and 500 says the wrong thing.
			return plugin.Text(http.StatusServiceUnavailable, "The drivers plugin did not answer; try again."), nil //nolint:nilerr // answered as a status, on purpose.
		case !known:
			return plugin.Text(http.StatusOK, "ignored: no driver called "+payment.Slug+" on this team"), nil
		}
	}

	// A real one would record the payment in this plugin's own tables, which
	// it has because its manifest asked for a database.
	return plugin.Text(http.StatusOK, "taken"), nil
}

// driverKnown asks the drivers plugin whether it knows this slug. The shape of
// the question and the answer are the drivers plugin's, documented beside it;
// the server carried them and read neither.
func (p payments) driverKnown(ctx context.Context, slug string) (bool, error) {
	hostMu.Lock()
	host := theHost
	hostMu.Unlock()
	if host == nil {
		return false, plugin.ErrUnavailable
	}
	question, err := json.Marshal(map[string]string{"slug": slug})
	if err != nil {
		return false, err
	}
	out, err := host.Ask(ctx, "drivers.lookup", question)
	if err != nil {
		return false, err
	}
	var answer struct {
		Known bool `json:"known"`
	}
	if err := json.Unmarshal(out, &answer); err != nil {
		return false, fmt.Errorf("the drivers plugin answered something this plugin cannot read: %w", err)
	}
	return answer.Known, nil
}

// page is the operator's own. Only an administrator of this server reaches it,
// and the host has already established that: r.Caller.AdminEmail is the host's
// word, read from its own session table and not from anything the browser sent.
func (p payments) page(_ context.Context, r plugin.HTTPRequest) (plugin.HTTPResponse, error) {
	plan := r.Settings.String(settingPlan)
	if plan == "" {
		plan = "Season pass"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "<h1>%s</h1>\n", html.EscapeString(plan))
	fmt.Fprintf(&b, "<p>Signed in as %s.</p>\n", html.EscapeString(r.Caller.AdminEmail))
	fmt.Fprintf(&b, "<p>The payment provider posts to <code>%s</code>.</p>\n",
		html.EscapeString(r.URL("/webhook")))
	b.WriteString("<p>Nobody has subscribed yet.</p>\n")
	return plugin.HTML(http.StatusOK, b.String()), nil
}

// signature is the provider's, over the body.
func signature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
