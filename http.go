package plugin

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// Putting a page on the operator's server.
//
// A plugin that implements [Server] is mounted at /plugin/<its name>/ and is
// handed every request made under it. The host forwards the request, takes back
// what to answer with, and writes it: the plugin never holds a connection, never
// binds a port, and never sees the socket.
//
// That shape is the point. A plugin is a separate process so that it can crash
// without taking the server with it, and a plugin serving its own listener would
// give that up — an operator would have two ports to open, two things to put
// behind their proxy, and a plugin that wedges would wedge a port rather than
// one request.

// Server is the optional half of the contract. A plugin implements it as well
// as [Plugin] to put pages or endpoints on the operator's server; one that does
// not simply does not implement it, and is never asked.
//
// A plugin that declares http in its manifest and does not implement this is
// refused at install, because the alternative is an operator following a link
// from their own panel to a 500.
type Server interface {
	// ServeHTTP answers one request. The deadline is on the context and the
	// host answers for a plugin that misses it, so a slow page is a slow page
	// and not a connection somebody else is waiting behind.
	//
	// It is named for [net/http.Handler] because that is the model — a request
	// in, a response out, and nothing held between them — and to keep it apart
	// from [Serve], which starts the process.
	ServeHTTP(ctx context.Context, r HTTPRequest) (HTTPResponse, error)
}

// Access is what the host requires of somebody before it forwards a request.
// It is declared in the manifest, per plugin rather than per route: a plugin
// that needs two different answers serves two different things, and the one
// that is public is the one worth being explicit about.
type Access string

const (
	// AccessPublic forwards anything, to anybody. It is for a plugin that
	// publishes something a league wants public — a leaderboard, a results
	// page somebody links to from a forum.
	AccessPublic Access = "public"
	// AccessDriver forwards only where the host holds a driver session, and
	// answers everybody else itself. The plugin is told which driver.
	AccessDriver Access = "driver"
	// AccessAdmin forwards only where the host holds an administrator session.
	// It is for a plugin that adds something to the operator's own tools.
	AccessAdmin Access = "admin"
	// AccessCustom forwards everything and leaves the decision to the plugin.
	//
	// It is what a plugin that signs drivers in needs, because its own sign-in
	// page has to be reachable by somebody who is not signed in yet. It is also
	// the one an operator should read twice before installing: the host is
	// checking nothing, and what the plugin publishes is what the internet
	// gets.
	AccessCustom Access = "custom"
)

// Valid reports whether a is one of the four.
func (a Access) Valid() bool {
	switch a {
	case AccessPublic, AccessDriver, AccessAdmin, AccessCustom:
		return true
	default:
		return false
	}
}

// Route is one address a plugin serves, and who may reach it.
//
// A route covers itself and everything under it: "/webhook" covers "/webhook"
// and "/webhook/stripe", and does not cover "/webhooks". The longest route that
// covers a path decides it, and a path no route covers is not served at all.
//
// That last rule is the one that makes a plugin reviewable. The declaration is
// not a promise the author makes about what their code does; it is a wall the
// host puts up in front of it. A path nobody thought about is refused rather
// than exposed, and somebody reading the manifest has read the whole of what
// this plugin puts on the operator's server.
type Route struct {
	// Path is the address, relative to the plugin's own prefix and beginning
	// with a slash. "/" covers everything the plugin serves.
	Path string `json:"path"`
	// Access is what the host requires of a caller before it forwards.
	Access Access `json:"access"`
	// Reason is why this route decides for itself. It is required for
	// [AccessCustom] and meaningless otherwise: that is the one mode where the
	// host checks nothing, so it is the one an operator has to be told about
	// in words before they enable the plugin.
	Reason string `json:"reason,omitempty"`
}

// Covers reports whether this route decides the given path.
func (r Route) Covers(path string) bool {
	route := strings.TrimSuffix(r.Path, "/")
	if route == "" {
		return true
	}
	return path == route || strings.HasPrefix(path, route+"/")
}

// HTTPCapability is what a plugin declares in its manifest to be given routes.
type HTTPCapability struct {
	// Title is what the panel calls the link to this plugin's pages. Empty
	// means the plugin's own name.
	Title string `json:"title,omitempty"`
	// Routes is every address this plugin serves. A plugin that declares none
	// is refused: asking for a URL without saying which is asking for all of
	// them.
	Routes []Route `json:"routes"`
}

// MaxRoutes is how many a plugin may declare. It is generous for anything with
// a reviewable surface and small enough that the list stays something a person
// reads rather than scrolls.
const MaxRoutes = 32

// For is the access required at a path, and whether this plugin serves it at
// all. The longest route that covers the path wins.
func (h HTTPCapability) For(path string) (Access, bool) {
	best := -1
	var access Access
	for _, r := range h.Routes {
		if !r.Covers(path) {
			continue
		}
		if n := len(strings.TrimSuffix(r.Path, "/")); n > best {
			best, access = n, r.Access
		}
	}
	return access, best >= 0
}

// Validate reports what is wrong with a declaration.
func (h HTTPCapability) Validate() error {
	switch {
	case len(h.Routes) == 0:
		return fmt.Errorf("%w: the plugin asks for a route and lists none — say which addresses it serves",
			ErrInvalid)
	case len(h.Routes) > MaxRoutes:
		return fmt.Errorf("%w: the plugin lists %d routes, and %d is the most a host will carry",
			ErrInvalid, len(h.Routes), MaxRoutes)
	case len(h.Title) > 60:
		return fmt.Errorf("%w: the title of a plugin's pages is longer than 60 characters", ErrInvalid)
	}
	seen := make(map[string]bool, len(h.Routes))
	for _, r := range h.Routes {
		switch {
		case !strings.HasPrefix(r.Path, "/"):
			return fmt.Errorf("%w: %q is not a route — one begins with a slash", ErrInvalid, r.Path)
		case len(r.Path) > 200:
			return fmt.Errorf("%w: %q is longer than a route may be", ErrInvalid, r.Path)
		case strings.Contains(r.Path, "//"), strings.Contains(r.Path, ".."):
			return fmt.Errorf("%w: %q is not a route a host will match", ErrInvalid, r.Path)
		case !r.Access.Valid():
			return fmt.Errorf("%w: %q is not a kind of access — it is public, driver, admin or custom",
				ErrInvalid, r.Access)
		case seen[strings.TrimSuffix(r.Path, "/")]:
			return fmt.Errorf("%w: %q is listed twice, so which access applies is a guess",
				ErrInvalid, r.Path)
		case r.Access == AccessCustom && strings.TrimSpace(r.Reason) == "":
			return fmt.Errorf(
				"%w: %q decides for itself who may reach it and gives no reason — that is the one "+
					"mode the host checks nothing in, and an operator is owed a sentence about it",
				ErrInvalid, r.Path)
		case len(r.Reason) > 200:
			return fmt.Errorf("%w: the reason given for %q is longer than 200 characters",
				ErrInvalid, r.Path)
		}
		seen[strings.TrimSuffix(r.Path, "/")] = true
	}
	return nil
}

// Describe is the route table in words, for the page an operator reads before
// they enable a plugin. Most plugins are never reviewed by anybody: this is
// what stands in for it.
func (h HTTPCapability) Describe() []string {
	out := make([]string, 0, len(h.Routes))
	for _, r := range h.Routes {
		line := "Serves " + r.Path + ", which " + reach(r.Access) + "."
		if r.Access == AccessCustom && r.Reason != "" {
			line += " It says why: " + r.Reason
		}
		out = append(out, line)
	}
	return out
}

// reach is who may reach a route, as a person reads it.
func reach(a Access) string {
	switch a {
	case AccessPublic:
		return "anyone may reach"
	case AccessDriver:
		return "any driver signed in to this server may reach"
	case AccessAdmin:
		return "only an administrator of this server may reach"
	case AccessCustom:
		return "anyone may reach, with the plugin itself deciding who may go on"
	default:
		return "nobody may reach, because this server does not understand who it is for"
	}
}

// Caller is what the host knows about whoever made a request. Every field is
// the host's own word: none of it comes from a header the caller can set, so a
// plugin can act on it.
type Caller struct {
	// DriverSlug and DriverName are the signed-in driver, or empty for nobody.
	// A driver is signed in only where the host holds a session for them, and
	// the host is the only thing that can mint one.
	DriverSlug string `json:"driver_slug,omitempty"`
	DriverName string `json:"driver_name,omitempty"`
	// AdminEmail is the signed-in operator, or empty.
	AdminEmail string `json:"admin_email,omitempty"`
	// Remote is where the request came from, as the host resolved it behind
	// whatever proxy the operator runs.
	Remote string `json:"remote,omitempty"`
}

// SignedIn reports whether a driver is signed in.
func (c Caller) SignedIn() bool { return c.DriverSlug != "" }

// HTTPRequest is one request, forwarded whole.
type HTTPRequest struct {
	// Method is the HTTP method.
	Method string `json:"method"`
	// Path is what follows the plugin's own prefix, always beginning with a
	// slash. A plugin routes on this.
	Path string `json:"path"`
	// Query is the raw query string, without the question mark.
	Query string `json:"query,omitempty"`
	// Header is the request's headers, minus the host's own: no cookie of the
	// host's reaches a plugin, and a plugin sees only cookies it set itself.
	Header http.Header `json:"header,omitempty"`
	// Body is the request body, already capped by the host.
	Body []byte `json:"body,omitempty"`
	// Caller is who made it.
	Caller Caller `json:"caller"`
	// Prefix is where this plugin is mounted — "/plugin/results" — so that it
	// can build links and form actions that come back to itself.
	Prefix string `json:"prefix"`
	// BaseURL is the operator's public address, for the links a plugin sends
	// somewhere else and expects a browser back from.
	BaseURL string `json:"base_url"`
	// Settings and Secrets are this plugin's configuration, on the same terms
	// as every other call.
	Settings Values  `json:"settings,omitempty"`
	Secrets  Secrets `json:"-"`
}

// URL is the address this request arrived at, for a plugin building an absolute
// link back to one of its own pages.
func (r HTTPRequest) URL(path string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return strings.TrimSuffix(r.BaseURL, "/") + r.Prefix + path
}

// HTTPResponse is what to answer with.
type HTTPResponse struct {
	// Status is the HTTP status code. Zero is 200.
	Status int `json:"status,omitempty"`
	// Header is what to write. The host refuses the headers that are its own
	// to decide, and lets a plugin set only its own cookies.
	Header http.Header `json:"header,omitempty"`
	// Body is the response body.
	Body []byte `json:"body,omitempty"`
	// SignIn asks the host to establish a driver session for this browser, by
	// the driver's slug.
	//
	// It is how a plugin that authenticates drivers hands the result back. The
	// plugin says who; the host checks the driver exists, mints the session and
	// sets the cookie. A plugin never sees a session token and cannot make one,
	// so the worst a broken one can do is name the wrong driver — which is bad,
	// and is not the same as forging sessions for a server it is not installed
	// on.
	SignIn string `json:"sign_in,omitempty"`
	// SignOut ends whatever driver session this browser has.
	SignOut bool `json:"sign_out,omitempty"`
	// Usage is what serving the request cost, if anything.
	Usage Usage `json:"usage,omitzero"`
}

// Text is a plain-text answer, for the plugin that wants one line rather than a
// template.
func Text(status int, body string) HTTPResponse {
	return HTTPResponse{
		Status: status,
		Header: http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
		Body:   []byte(body),
	}
}

// HTML is an answer that is a page.
func HTML(status int, body string) HTTPResponse {
	return HTTPResponse{
		Status: status,
		Header: http.Header{"Content-Type": {"text/html; charset=utf-8"}},
		Body:   []byte(body),
	}
}

// Redirect sends the browser somewhere else.
func Redirect(status int, to string) HTTPResponse {
	return HTTPResponse{Status: status, Header: http.Header{"Location": {to}}}
}
