# payments

An example of a plugin that serves two addresses on the operator's server and
needs a different answer at each.

    /plugin/payments/webhook    public   the payment provider posts here
    /plugin/payments/           admin    the operator's own page

It is the shape that made routes worth declaring one at a time. A payment
provider has to be able to post to the webhook: there is no browser, no session
and no operator, just another machine with a signing key. The pages beside it
list who has paid and can refund somebody, and nobody but the operator should
see them. One answer for both addresses is either a webhook that never arrives
or a refund button on the open internet.

The host enforces the table. Anything not under `/webhook` or `/` is refused
without this plugin being asked, and the two that are listed are checked before
anything is forwarded — so there is no authentication code in this plugin at
all. What it does instead is the thing only it can do: check that the webhook
really came from the provider, by the signature the provider sent.

Nothing here talks to a real payment provider. The signature check is real and
the rest is a stub, because what this example is for is the shape.
