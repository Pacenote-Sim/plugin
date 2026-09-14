package plugin_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pacenote-sim/plugin"
)

// The route table, which is the only thing a host enforces about what a plugin
// puts on an operator's server.
//
// It is not a promise the author makes about their own code: the host refuses a
// path no route covers, without asking the plugin. So these tests are mostly
// about what is *not* served — a path nobody declared, a path that looks like
// one that was, a plugin that asked for a URL and named none.

func TestWhichRouteDecidesAPath(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	h := plugin.HTTPCapability{Routes: []plugin.Route{
		{Path: "/", Access: plugin.AccessAdmin},
		{Path: "/webhook", Access: plugin.AccessPublic},
		{Path: "/webhook/test", Access: plugin.AccessAdmin},
	}}

	// The longest route that covers a path decides it.
	for path, want := range map[string]plugin.Access{
		"/":                    plugin.AccessAdmin,
		"/refunds":             plugin.AccessAdmin,
		"/webhook":             plugin.AccessPublic,
		"/webhook/stripe":      plugin.AccessPublic,
		"/webhook/test":        plugin.AccessAdmin,
		"/webhook/test/replay": plugin.AccessAdmin,
		// A path that merely starts with the same letters is a different path.
		// Matching on characters rather than on segments is how "/webhooks"
		// quietly becomes public.
		"/webhooks": plugin.AccessAdmin,
	} {
		got, ok := h.For(path)
		r.True(ok, "%s is served by nothing", path)
		r.Equal(want, got, "%s", path)
	}
}

// A plugin that does not list "/" serves only what it listed. Everything else
// is refused by the host, which is the whole point of the declaration.
func TestAPathNoRouteCoversIsNotServed(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	h := plugin.HTTPCapability{Routes: []plugin.Route{
		{Path: "/webhook", Access: plugin.AccessPublic},
	}}

	_, ok := h.For("/webhook")
	r.True(ok)
	for _, path := range []string{"/", "/admin", "/webhooks", "/web"} {
		_, ok := h.For(path)
		r.False(ok, "%s was served by a plugin that did not declare it", path)
	}
}

func TestWhatIsNotARouteTable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		http plugin.HTTPCapability
		want string
	}{
		{
			name: "no routes at all", http: plugin.HTTPCapability{},
			want: "asks for a route and lists none",
		},
		{
			name: "a path that is not one",
			http: plugin.HTTPCapability{Routes: []plugin.Route{{Path: "webhook", Access: plugin.AccessPublic}}},
			want: "begins with a slash",
		},
		{
			name: "a path that climbs out",
			http: plugin.HTTPCapability{Routes: []plugin.Route{{Path: "/../admin", Access: plugin.AccessPublic}}},
			want: "not a route a host will match",
		},
		{
			name: "an access nobody has heard of",
			http: plugin.HTTPCapability{Routes: []plugin.Route{{Path: "/", Access: "whoever"}}},
			want: "not a kind of access",
		},
		{
			name: "the same path twice",
			http: plugin.HTTPCapability{Routes: []plugin.Route{
				{Path: "/", Access: plugin.AccessPublic},
				{Path: "/", Access: plugin.AccessAdmin},
			}},
			want: "listed twice",
		},
		{
			// The one mode the host checks nothing in. An operator enabling it
			// is owed a sentence about why, because nothing else will tell them.
			name: "custom with no reason given",
			http: plugin.HTTPCapability{Routes: []plugin.Route{{Path: "/", Access: plugin.AccessCustom}}},
			want: "gives no reason",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.http.Validate()
			require.ErrorIs(t, err, plugin.ErrInvalid)
			require.Contains(t, err.Error(), tc.want)
		})
	}

	// And one that is fine.
	require.NoError(t, plugin.HTTPCapability{Title: "Payments", Routes: []plugin.Route{
		{Path: "/webhook", Access: plugin.AccessPublic},
		{Path: "/", Access: plugin.AccessAdmin},
	}}.Validate())
}

// What an operator reads before they enable a plugin. Most plugins are never
// reviewed by anybody, so this list is what stands in for it.
func TestTheRouteTableInWords(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	lines := plugin.HTTPCapability{Routes: []plugin.Route{
		{Path: "/webhook", Access: plugin.AccessPublic},
		{Path: "/", Access: plugin.AccessAdmin},
		{Path: "/me", Access: plugin.AccessDriver},
		{
			Path: "/sign-in", Access: plugin.AccessCustom,
			Reason: "The sign-in page has to be reachable by somebody not yet signed in.",
		},
	}}.Describe()

	r.Len(lines, 4)
	r.Contains(lines[0], "/webhook")
	r.Contains(lines[0], "anyone may reach")
	r.Contains(lines[1], "only an administrator")
	r.Contains(lines[2], "any driver signed in")
	r.Contains(lines[3], "deciding who may go on")
	r.Contains(lines[3], "not yet signed in", "the operator is not told why it decides for itself")
}

// The small constructors a plugin actually writes its handler with. They are
// here because getting a content type wrong is the kind of mistake that only
// shows up as a browser rendering markup as text.
func TestTheAnswersAPluginCanBuild(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	text := plugin.Text(200, "ok")
	r.Equal(200, text.Status)
	r.Equal("text/plain; charset=utf-8", text.Header.Get("Content-Type"))
	r.Equal("ok", string(text.Body))

	page := plugin.HTML(404, "<p>no</p>")
	r.Equal(404, page.Status)
	r.Equal("text/html; charset=utf-8", page.Header.Get("Content-Type"))
	r.Equal("<p>no</p>", string(page.Body))

	away := plugin.Redirect(303, "/plugin/results/")
	r.Equal(303, away.Status)
	r.Equal("/plugin/results/", away.Header.Get("Location"))
	r.Empty(away.Body, "a redirect with a body is a redirect nobody reads")
}

// A plugin builds links back to itself from what the host told it, because it
// does not know its own address.
func TestTheAddressAPluginBuildsForItself(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	req := plugin.HTTPRequest{Prefix: "/plugin/results", BaseURL: "https://pacenote.example.com"}
	r.Equal("https://pacenote.example.com/plugin/results/standings", req.URL("standings"),
		"a path with no leading slash was not given one")
	r.Equal("https://pacenote.example.com/plugin/results/standings", req.URL("/standings"))
	r.Equal("https://pacenote.example.com/plugin/results/", req.URL("/"))

	// A base address with a trailing slash is the same address.
	trailing := plugin.HTTPRequest{Prefix: "/plugin/results", BaseURL: "https://pacenote.example.com/"}
	r.Equal("https://pacenote.example.com/plugin/results/standings", trailing.URL("standings"))
}

// Who the caller is, as a plugin asks it.
func TestWhetherADriverIsSignedIn(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	r.False(plugin.Caller{}.SignedIn())
	r.False(plugin.Caller{AdminEmail: "ana@example.com"}.SignedIn(),
		"an administrator is not a driver")
	r.True(plugin.Caller{DriverSlug: "ana-lopez"}.SignedIn())
}

// The limits on a route table. A host carries what a plugin declares, so what
// it may declare is bounded before the host ever sees it.
func TestTheLimitsOnARouteTable(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	tooMany := make([]plugin.Route, plugin.MaxRoutes+1)
	for i := range tooMany {
		tooMany[i] = plugin.Route{Path: "/" + strconv.Itoa(i), Access: plugin.AccessAdmin}
	}
	r.ErrorIs(plugin.HTTPCapability{Routes: tooMany}.Validate(), plugin.ErrInvalid)
	r.NoError(plugin.HTTPCapability{Routes: tooMany[:plugin.MaxRoutes]}.Validate(),
		"%d routes is the limit, not one under it", plugin.MaxRoutes)

	one := []plugin.Route{{Path: "/", Access: plugin.AccessAdmin}}
	r.ErrorIs(plugin.HTTPCapability{Title: strings.Repeat("t", 61), Routes: one}.Validate(), plugin.ErrInvalid)
	r.NoError(plugin.HTTPCapability{Title: strings.Repeat("t", 60), Routes: one}.Validate())

	long := "/" + strings.Repeat("p", 200)
	r.ErrorIs(plugin.HTTPCapability{Routes: []plugin.Route{
		{Path: long, Access: plugin.AccessAdmin},
	}}.Validate(), plugin.ErrInvalid)

	r.ErrorIs(plugin.HTTPCapability{Routes: []plugin.Route{
		{Path: "/", Access: plugin.AccessCustom, Reason: strings.Repeat("r", 201)},
	}}.Validate(), plugin.ErrInvalid)
}

// An access this build does not understand is described as reaching nobody,
// because the panel has to say something and "anyone" is the wrong guess.
func TestAnAccessThisBuildDoesNotUnderstand(t *testing.T) {
	t.Parallel()

	line := plugin.HTTPCapability{Routes: []plugin.Route{
		{Path: "/x", Access: plugin.Access("from-the-future")},
	}}.Describe()
	require.Contains(t, strings.Join(line, " "), "nobody may reach")
}
