# drivers

An example plugin other plugins ask. It knows which drivers are on the team — a list the operator
types into its settings — and answers two questions:

| Kind | Question | Answer |
|---|---|---|
| `drivers.lookup` | `{"slug": "ana-ruiz"}` | `{"slug": "ana-ruiz", "known": true, "name": "Ana Ruiz"}` |
| `drivers.list` | none | `{"drivers": [{"slug": ..., "name": ...}, ...]}` |

The kinds are spelled with this plugin's name, so a question carries the name of the plugin that
answers it and the server needs no table. The payloads are this plugin's to define, which is why
they are documented here: the server carries them and reads neither.

`examples/payments` is the plugin that asks it.
