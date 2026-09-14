# Pacenote plugin interface

The contract between a Pacenote server and a plugin. It holds the interface and nothing else — no
host, no implementation, no database — so a plugin author depends on this module and never on a
server.

**Licensed Apache-2.0**, while the Pacenote server is GPL-3. A plugin is a separate process talking
to the server over a local channel, so it is not linked into the server and is not a derivative work
of it. You can license your plugin however you like, including closed.

## What a plugin can do

| | |
|---|---|
| **Be told something happened** | `lap.completed`, `stint.finished` — derived facts, never raw traces. Fire and forget. |
| **Be asked for something** | `cue.race`, `cue.training`, `debrief`, `setup`, `speak`, with a deadline the host means. Miss it and you are skipped. |
| **Be lent a credential** | For one call. The core holds it; you never store one. |
| **Report what a call did** | The job it was, whether it came from your cache, and any tokens it spent. The core records it and enforces the daily cap. |
| **Declare your settings** | The panel renders them, so an operator configures you in one place. |
| **Keep tables of your own** | Your own PostgreSQL role and schema, your own migrations, dropped when you are uninstalled. |
| **Serve your own pages** | At `/plugin/<your name>/`, with the host deciding who may reach each one. |

## Serving pages

A plugin that implements `Server` is mounted at `/plugin/<its name>/` on the server's own port.
There is no second listener.

Every address is declared in the manifest, and the declaration is what the host enforces:

```json
{
  "capabilities": {
    "http": {
      "title": "Payments",
      "routes": [
        { "path": "/webhook", "access": "public" },
        { "path": "/",        "access": "admin"  }
      ]
    }
  }
}
```

| Access | Who reaches it |
|---|---|
| `public` | Anyone. A payment provider's webhook, a page with no session behind it. |
| `driver` | A driver signed in to this league. |
| `admin` | An administrator of this server. |
| `custom` | You check it yourself, and say why in `reason`. |

An address no route covers never reaches the plugin. The longest matching route decides, so
`/webhook` stays `public` even though `/` is `admin`.

What the host does not pass on:

- Its own cookies. A plugin sees only cookies it set itself, and never another plugin's.
- Who the caller is, from anything the browser sent. That comes from the server's sessions.
- An unbounded request or response. Both are capped, and the call has a deadline.

`examples/payments` is a worked example: a public webhook checked with an HMAC, and an operator-only
page beside it.

## Writing one

```go
package main

import (
	"context"

	"github.com/pacenote-sim/plugin"
)

type mine struct{}

func (mine) Settings(context.Context) ([]plugin.Setting, error) { return nil, nil }
func (mine) Notify(context.Context, plugin.Event) (plugin.Usage, error) { return plugin.Usage{}, nil }
func (mine) Answer(context.Context, plugin.Request) (plugin.Response, error) {
	return plugin.Response{}, plugin.ErrUnsupported
}

func main() { plugin.Serve(mine{}) }
```

Put a `plugin.json` beside the binary, in a directory named after the plugin, under the server's
`<data>/plugins/`. The manifest, the rules and a complete worked example are the package
documentation: `go doc github.com/pacenote-sim/plugin`.

`examples/testplugin` exercises every part of the contract and does nothing else. It is what a host
tests against, and the shortest complete example to read first.

## Versioning

`plugin.InterfaceVersion` is this contract's version. A host declares it, a plugin declares in its
manifest the one it was built against, and they must match **exactly**. A mismatch refuses to start
with a message naming both versions. There is no degraded mode: a plugin missing a fact it was
never sent fails later, and somewhere else.

## The transport

gRPC over a local socket, via [hashicorp/go-plugin](https://github.com/hashicorp/go-plugin). The
message bodies are JSON documents rather than protobuf messages, because this product already speaks
JSON between its client and its server and two schemas drift. `proto/plugin.proto` and the Go types
in this module are both published, so a plugin in another language has everything it needs.

Credentials are the exception: they travel in their own field and never inside a JSON body, so a
plugin author who logs the request they were given cannot leak an operator's key by accident.

## Checks

```
make check    # fmt, build, vet, lint, test, tidy
make cover    # every package over 90%
```

## Licence

**Apache License 2.0.** Full text in `LICENSE`.
