package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ManifestName is the file that declares a plugin, beside its binary. A
// directory under the host's plugin directory with one of these in it is a
// plugin; a directory without one is ignored and reported, never guessed at.
const ManifestName = "plugin.json"

// Capabilities are what a plugin says it does, shown to the operator before
// they install it and again on its page afterwards.
//
// Nothing here is enforced, and that is worth saying plainly rather than
// implying a declaration is a sandbox. A plugin is a process running with the
// server's privileges, and an operator who installs one is trusting its author.
// What the declaration buys is an informed decision and an honest page, which
// is not nothing: an integration that says it makes no network calls and then
// does is a plugin nobody will list again.
type Capabilities struct {
	// Events are the kinds this plugin wants delivered. The host sends these
	// and no others.
	Events []EventKind `json:"events,omitempty"`
	// Requests are the kinds it will answer, each spelled "<this plugin's
	// name>.<what>". The host refuses to ask for anything else rather than
	// waiting out a deadline to find out.
	Requests []RequestKind `json:"requests,omitempty"`
	// Asks names the plugins this one asks questions of. It is a declaration
	// the operator reads at install — "payments asks drivers for information"
	// — and one the host enforces: a question to a plugin not named here is
	// refused before it is put.
	Asks []string `json:"asks,omitempty"`
	// Network reports that it calls something outside this machine. It is the
	// declaration that matters most, because it is the one that turns the
	// operator's data into somebody else's.
	Network bool `json:"network"`
	// WritesFiles reports that it writes to disk.
	WritesFiles bool `json:"writes_files"`
	// ReadsDriverData reports that it uses the driver's name and record rather
	// than anonymous numbers.
	ReadsDriverData bool `json:"reads_driver_data"`
	// Database asks for tables of its own. The host creates a PostgreSQL role
	// and a schema for this plugin alone, applies whatever is in its
	// [MigrationsDir], and hands it the connection string in [EnvDatabaseURL].
	//
	// Unlike everything else here, this one is enforced. A plugin that does not
	// declare it is given no database and no role exists for it; a plugin that
	// does gets one it owns entirely and cannot reach out of. Uninstalling
	// drops both, so a plugin's data leaves with the plugin.
	Database bool `json:"database,omitempty"`
	// HTTP asks for a route of this plugin's own, at /plugin/<name>/. A plugin
	// that declares it must implement [Server]; one that does not is refused at
	// install rather than left as a link in the panel that answers 500.
	//
	// Like [Capabilities.Database] this one is enforced. A plugin that does not
	// declare it has no route and is never handed a request, so the surface a
	// plugin adds to the operator's server is the surface it asked for in
	// writing.
	HTTP *HTTPCapability `json:"http,omitempty"`
}

// Wants reports whether this plugin asked for the event.
func (c Capabilities) Wants(k EventKind) bool {
	for _, e := range c.Events {
		if e == k {
			return true
		}
	}
	return false
}

// Answers reports whether this plugin says it answers the request.
func (c Capabilities) Answers(k RequestKind) bool {
	for _, r := range c.Requests {
		if r == k {
			return true
		}
	}
	return false
}

// MayAsk reports whether this plugin declared that it asks the named plugin.
func (c Capabilities) MayAsk(target string) bool {
	for _, a := range c.Asks {
		if a == target {
			return true
		}
	}
	return false
}

// Describe is the capability list as an operator reads it, one sentence each.
// It is here rather than in the panel so that every host words it the same way.
func (c Capabilities) Describe() []string {
	var out []string
	if len(c.Events) > 0 {
		kinds := make([]string, len(c.Events))
		for i, e := range c.Events {
			kinds[i] = string(e)
		}
		out = append(out, "Is told when: "+strings.Join(kinds, ", "))
	}
	if len(c.Requests) > 0 {
		kinds := make([]string, len(c.Requests))
		for i, r := range c.Requests {
			kinds[i] = string(r)
		}
		out = append(out, "Answers: "+strings.Join(kinds, ", "))
	}
	if len(c.Asks) > 0 {
		out = append(out, "Asks "+strings.Join(c.Asks, ", ")+" for information.")
	}
	if c.Network {
		out = append(out, "Calls something outside this machine.")
	}
	if c.WritesFiles {
		out = append(out, "Writes files.")
	}
	if c.ReadsDriverData {
		out = append(out, "Reads drivers' names and records.")
	}
	if c.Database {
		out = append(out, "Keeps tables of its own, which are removed when it is.")
	}
	if c.HTTP != nil {
		// Every address, one line each. It is the longest part of this list on
		// a plugin that serves pages, and deliberately so: it is the only part
		// the host actually enforces, so it is the part worth reading.
		out = append(out, c.HTTP.Describe()...)
	}
	if len(out) == 0 {
		out = append(out, "Declares nothing.")
	}
	return out
}

// Validate refuses a capability list that names something this interface
// version does not carry, which is nearly always a plugin built against another
// version whose manifest was edited by hand.
func (c Capabilities) Validate() error {
	for _, e := range c.Events {
		if !e.Valid() {
			return fmt.Errorf("%w: %q is not an event this server carries", ErrInvalid, e)
		}
	}
	for _, r := range c.Requests {
		if !r.Valid() {
			return fmt.Errorf("%w: %q is not a request kind — spell it <plugin>.<what>", ErrInvalid, r)
		}
	}
	for _, a := range c.Asks {
		if !validName(a) {
			return fmt.Errorf("%w: %q is not a plugin name, so it cannot be asked anything", ErrInvalid, a)
		}
	}
	if c.HTTP != nil {
		if err := c.HTTP.Validate(); err != nil {
			return err
		}
	}
	if len(c.Events) == 0 && len(c.Requests) == 0 && c.HTTP == nil {
		return fmt.Errorf("%w: the plugin asks for no events, answers no requests and serves no pages, so nothing would ever call it", ErrInvalid)
	}
	return nil
}

// Serves reports whether this plugin asked for a route.
func (c Capabilities) Serves() bool { return c.HTTP != nil }

// Manifest is the file beside the binary: what this plugin is, what it was
// built against, and what it does.
type Manifest struct {
	// Name identifies the plugin everywhere — the directory, the settings, the
	// metering rows, the panel. Lowercase letters, digits, underscores and
	// hyphens, and permanent: renaming one is installing a different plugin.
	Name string `json:"name"`
	// Version is the plugin's own version, for the operator and the
	// marketplace. Its spelling is the author's business.
	Version string `json:"version"`
	// Author is who to blame, and who the operator is trusting.
	Author string `json:"author"`
	// Description is one line of what it does, shown in the panel.
	Description string `json:"description"`
	// InterfaceVersion is the version of this contract the plugin was built
	// against. It must equal the host's [InterfaceVersion] exactly; see
	// [CheckVersion].
	InterfaceVersion int `json:"interface_version"`
	// Binary is the executable to run, relative to the manifest and with no
	// directory separators in it. Empty means [Manifest.Name]; on Windows a
	// ".exe" is added when it is not already there.
	Binary string `json:"binary,omitempty"`
	// Capabilities are what it does.
	Capabilities Capabilities `json:"capabilities"`
}

// ParseManifest reads a manifest and checks it. An unknown field is refused
// rather than ignored: a manifest with "capabilitys" in it is a plugin that
// will start and then quietly receive nothing, and the author needs to be told
// at the point they can still fix it.
func ParseManifest(b []byte) (Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("%w: %s is not readable: %w", ErrInvalid, ManifestName, err)
	}
	if dec.More() {
		return Manifest{}, fmt.Errorf("%w: %s carries more than one document", ErrInvalid, ManifestName)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// LoadManifest reads the manifest in dir.
func LoadManifest(dir string) (Manifest, error) {
	path := filepath.Join(dir, ManifestName)
	// The path is inside the operator's own plugin directory, which is the
	// whole job of this function.
	b, err := os.ReadFile(path) //nolint:gosec // G304: the plugin directory is the operator's own.
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: %s could not be read: %w", ErrInvalid, path, err)
	}
	m, err := ParseManifest(b)
	if err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// Validate refuses a manifest that cannot be installed.
func (m Manifest) Validate() error {
	switch {
	case !validName(m.Name):
		return fmt.Errorf("%w: %q is not a plugin name — use lowercase letters, digits, underscores and hyphens", ErrInvalid, m.Name)
	case strings.TrimSpace(m.Version) == "":
		return fmt.Errorf("%w: %s declares no version", ErrInvalid, m.Name)
	case strings.TrimSpace(m.Author) == "":
		return fmt.Errorf("%w: %s declares no author, and an operator installing it is trusting one", ErrInvalid, m.Name)
	case strings.TrimSpace(m.Description) == "":
		return fmt.Errorf("%w: %s describes itself in no words, so the panel has nothing to show", ErrInvalid, m.Name)
	case m.InterfaceVersion <= 0:
		return fmt.Errorf("%w: %s declares no plugin interface version, so there is no way to know what it was built against", ErrInvalid, m.Name)
	case strings.ContainsAny(m.Binary, `/\`):
		return fmt.Errorf("%w: the binary of %s must be a file name beside the manifest, not a path", ErrInvalid, m.Name)
	}
	if err := m.Capabilities.Validate(); err != nil {
		return err
	}
	// A plugin's kinds are spelled with its own name, so the kind says who
	// answers it and two plugins cannot collide; and a plugin does not ask
	// itself, which is the shortest loop there is.
	for _, r := range m.Capabilities.Requests {
		if r.Plugin() != m.Name {
			return fmt.Errorf("%w: %s cannot answer %q — a plugin's kinds are spelled <its name>.<what>", ErrInvalid, m.Name, r)
		}
	}
	for _, a := range m.Capabilities.Asks {
		if a == m.Name {
			return fmt.Errorf("%w: %s declares that it asks itself, which is a loop and not a capability", ErrInvalid, m.Name)
		}
	}
	return nil
}

// Executable is the file to run, given the directory the manifest was read
// from. It is the manifest's binary, or the plugin's name, with the platform's
// extension.
func (m Manifest) Executable(dir string) string {
	name := m.Binary
	if name == "" {
		name = m.Name
	}
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(name), ".exe") {
		name += ".exe"
	}
	return filepath.Join(dir, name)
}
