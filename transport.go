package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/pacenote-sim/plugin/internal/pb"
)

// The transport. Both halves live here because they are one thing: what the
// host sends has to be exactly what the plugin receives, and splitting them
// over two files is how that stops being true.
//
// Bodies are JSON, carried inside the protobuf messages rather than modelled as
// them. That is the same choice the product already made between the client and
// the server, it keeps one schema to document instead of two that can drift,
// and it leaves the door open for a plugin written in another language because
// the .proto and the JSON shapes are both published.
//
// Credentials never travel inside those bodies. They have their own field, they
// are put into it explicitly by [Secrets.raw], and the [Secret] type makes it
// impossible for one to reach the JSON by accident.
//
// Two services cross the connection. Plugin is the host calling the plugin,
// over the connection go-plugin opened. Host is the plugin calling the server,
// over a second channel go-plugin's broker multiplexes onto the same
// connection: the server serves one Host per plugin, so it knows who is asking
// from which channel the question arrived on and never from a field.

// registerServer wires impl into the plugin process's gRPC server.
func registerServer(s *grpc.Server, impl Plugin, broker *goplugin.GRPCBroker) {
	pb.RegisterPluginServer(s, &server{impl: impl, broker: broker})
}

// server is the plugin's side: protobuf in, Go out, and back.
type server struct {
	pb.UnimplementedPluginServer
	impl   Plugin
	broker *goplugin.GRPCBroker

	// connect runs once: the first Settings call carries the host's broker id,
	// and a plugin that asks is connected to it before it says what it needs.
	connect sync.Once
	connErr error
}

func (s *server) Settings(ctx context.Context, in *pb.SettingsRequest) (*pb.SettingsResponse, error) {
	if err := s.connectHost(in.GetHostBrokerId()); err != nil {
		return nil, toStatus(err)
	}
	settings, err := s.impl.Settings(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	if bad := ValidateSettings(settings); bad != nil {
		return nil, toStatus(bad)
	}
	b, err := json.Marshal(settings)
	if err != nil {
		return nil, toStatus(fmt.Errorf("%w: the settings declaration cannot be encoded: %w", ErrInvalid, err))
	}
	return &pb.SettingsResponse{SettingsJson: b}, nil
}

// connectHost hands an [Asker] the host, once. A plugin that does not ask is
// not connected to anything, whatever the host offered; a host that offers
// nothing leaves an asker with nobody to ask, and the first question says so.
func (s *server) connectHost(brokerID uint32) error {
	asker, ok := s.impl.(Asker)
	if !ok || brokerID == 0 || s.broker == nil {
		return nil
	}
	s.connect.Do(func() {
		conn, err := s.broker.Dial(brokerID)
		if err != nil {
			s.connErr = fmt.Errorf("%w: the host's channel could not be reached: %w", ErrUnavailable, err)
			return
		}
		asker.Connected(&hostClient{c: pb.NewHostClient(conn)})
	})
	return s.connErr
}

func (s *server) Notify(ctx context.Context, in *pb.NotifyRequest) (*pb.NotifyResponse, error) {
	var e Event
	if err := json.Unmarshal(in.GetEventJson(), &e); err != nil {
		return nil, toStatus(fmt.Errorf("%w: the event is not readable: %w", ErrInvalid, err))
	}
	if err := json.Unmarshal(orEmptyObject(in.GetValuesJson()), &e.Settings); err != nil {
		return nil, toStatus(fmt.Errorf("%w: the settings are not readable: %w", ErrInvalid, err))
	}
	e.Secrets = secretsFrom(in.GetSecrets())
	if err := e.Validate(); err != nil {
		return nil, toStatus(err)
	}
	use, err := s.impl.Notify(ctx, e)
	if err != nil {
		return nil, toStatus(err)
	}
	if bad := use.Validate(); bad != nil {
		return nil, toStatus(bad)
	}
	b, err := json.Marshal(use)
	if err != nil {
		return nil, toStatus(fmt.Errorf("%w: what that event cost cannot be encoded: %w", ErrInvalid, err))
	}
	return &pb.NotifyResponse{UsageJson: b}, nil
}

// Answer is the waiting half. Answering is optional, so this is where a
// manifest that declares requests and a binary that does not answer them meet:
// the asker is told the plugin does not answer, rather than being handed an
// empty document.
func (s *server) Answer(ctx context.Context, in *pb.AnswerRequest) (*pb.AnswerResponse, error) {
	impl, ok := s.impl.(Answerer)
	if !ok {
		return nil, status.Error(codes.Unimplemented,
			"this plugin declared requests in its manifest and does not answer them")
	}
	var r Request
	if err := json.Unmarshal(in.GetRequestJson(), &r); err != nil {
		return nil, toStatus(fmt.Errorf("%w: the request is not readable: %w", ErrInvalid, err))
	}
	if err := json.Unmarshal(orEmptyObject(in.GetValuesJson()), &r.Settings); err != nil {
		return nil, toStatus(fmt.Errorf("%w: the settings are not readable: %w", ErrInvalid, err))
	}
	r.Secrets = secretsFrom(in.GetSecrets())
	if err := r.Validate(); err != nil {
		return nil, toStatus(err)
	}
	// A question this plugin asks while answering is counted from here.
	res, err := impl.Answer(withHops(ctx, r.Hops), r)
	if err != nil {
		return nil, toStatus(err)
	}
	if res.Kind == "" {
		res.Kind = r.Kind
	}
	if bad := res.Validate(); bad != nil {
		return nil, toStatus(bad)
	}
	b, err := json.Marshal(res)
	if err != nil {
		return nil, toStatus(fmt.Errorf("%w: the answer cannot be encoded: %w", ErrInvalid, err))
	}
	return &pb.AnswerResponse{ResponseJson: b}, nil
}

// client is the host's side: a [Plugin] that is another process.
type client struct {
	c      pb.PluginClient
	broker *goplugin.GRPCBroker

	mu       sync.Mutex
	brokerID uint32
	// hostSrv serves [Host] to this plugin once attached; detach stops it.
	hostSrv *grpc.Server
}

// newClient wraps a connection to a running plugin.
func newClient(cc *grpc.ClientConn, broker *goplugin.GRPCBroker) Plugin {
	return &client{c: pb.NewPluginClient(cc), broker: broker}
}

// attach serves h to this plugin over the broker and remembers where, so the
// next Settings call can say. It is what [Attach] does for a real connection.
func (c *client) attach(h Host) bool {
	if c.broker == nil || h == nil {
		return false
	}
	s := grpc.NewServer()
	pb.RegisterHostServer(s, &hostServer{impl: h})
	id := c.broker.NextId()

	c.mu.Lock()
	c.brokerID, c.hostSrv = id, s
	c.mu.Unlock()

	go func() {
		// Accept rather than go-plugin's AcceptAndServe, because the latter
		// logs through the standard logger when the broker closes under it —
		// which is every time a plugin stops. A plugin that never dials is a
		// plugin that does not ask; Accept returns when the broker goes. Serve
		// does not — the multiplexed listener blocks past the broker's close —
		// which is what detach is for.
		ln, err := c.broker.Accept(id)
		if err != nil {
			return
		}
		_ = s.Serve(ln)
	}()
	return true
}

// detach stops serving [Host] to this plugin. It is what [Attach] hands the
// server, and the server calls it when the plugin is stopped.
func (c *client) detach() {
	c.mu.Lock()
	s := c.hostSrv
	c.hostSrv = nil
	c.mu.Unlock()
	if s != nil {
		s.Stop()
	}
}

func (c *client) Settings(ctx context.Context) ([]Setting, error) {
	c.mu.Lock()
	id := c.brokerID
	c.mu.Unlock()
	out, err := c.c.Settings(ctx, &pb.SettingsRequest{HostBrokerId: id})
	if err != nil {
		return nil, fromStatus(err)
	}
	var settings []Setting
	if err := json.Unmarshal(orEmptyArray(out.GetSettingsJson()), &settings); err != nil {
		return nil, fmt.Errorf("%w: the settings declaration is not readable: %w", ErrInvalid, err)
	}
	if err := ValidateSettings(settings); err != nil {
		return nil, err
	}
	return settings, nil
}

func (c *client) Notify(ctx context.Context, e Event) (Usage, error) {
	body, err := json.Marshal(e)
	if err != nil {
		return Usage{}, fmt.Errorf("%w: the event cannot be encoded: %w", ErrInvalid, err)
	}
	values, err := json.Marshal(e.Settings)
	if err != nil {
		return Usage{}, fmt.Errorf("%w: the settings cannot be encoded: %w", ErrInvalid, err)
	}
	out, err := c.c.Notify(ctx, &pb.NotifyRequest{
		EventJson:  body,
		ValuesJson: values,
		Secrets:    e.Secrets.raw(),
	})
	if err != nil {
		return Usage{}, fromStatus(err)
	}
	var use Usage
	if err := json.Unmarshal(orEmptyObject(out.GetUsageJson()), &use); err != nil {
		return Usage{}, fmt.Errorf("%w: what that event cost is not readable: %w", ErrInvalid, err)
	}
	if err := use.Validate(); err != nil {
		return Usage{}, err
	}
	return use, nil
}

// Answer is the host's side of asking. It is named to satisfy [Answerer], so
// the host holds one type for a plugin whatever it does.
func (c *client) Answer(ctx context.Context, r Request) (Response, error) {
	body, err := json.Marshal(r)
	if err != nil {
		return Response{}, fmt.Errorf("%w: the request cannot be encoded: %w", ErrInvalid, err)
	}
	values, err := json.Marshal(r.Settings)
	if err != nil {
		return Response{}, fmt.Errorf("%w: the settings cannot be encoded: %w", ErrInvalid, err)
	}
	out, err := c.c.Answer(ctx, &pb.AnswerRequest{
		RequestJson: body,
		ValuesJson:  values,
		Secrets:     r.Secrets.raw(),
	})
	if err != nil {
		return Response{}, fromStatus(err)
	}
	var res Response
	if err := json.Unmarshal(orEmptyObject(out.GetResponseJson()), &res); err != nil {
		return Response{}, fmt.Errorf("%w: the answer is not readable: %w", ErrInvalid, err)
	}
	if err := res.Validate(); err != nil {
		return Response{}, err
	}
	return res, nil
}

// hostServer is the server's side of a plugin asking: it runs in the server
// process, one per plugin, and hands each question to the [Host] the server
// attached.
type hostServer struct {
	pb.UnimplementedHostServer
	impl Host
}

func (h *hostServer) Ask(ctx context.Context, in *pb.HostAskRequest) (*pb.HostAskResponse, error) {
	hops := int(min(in.GetHops(), uint32(MaxHops+1)))
	out, err := h.impl.Ask(withHops(ctx, hops), in.GetKind(), in.GetPayload())
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.HostAskResponse{Payload: out}, nil
}

// hostClient is the plugin's side of asking: the [Host] an [Asker] is handed.
type hostClient struct{ c pb.HostClient }

func (h *hostClient) Ask(ctx context.Context, kind string, payload json.RawMessage) (json.RawMessage, error) {
	out, err := h.c.Ask(ctx, &pb.HostAskRequest{
		Kind:    kind,
		Payload: payload,
		Hops:    uint32(max(Hops(ctx), 0)), //nolint:gosec // G115: bounded by MaxHops on the way in.
	})
	if err != nil {
		return nil, fromStatus(err)
	}
	return out.GetPayload(), nil
}

// remote is an error that happened in the other process. It keeps the sentinel
// so a caller can branch on it, and the plugin's own words so an operator can
// read them.
type remote struct {
	sentinel error
	message  string
}

func (e *remote) Error() string { return e.message }
func (e *remote) Unwrap() error { return e.sentinel }

// The sentinels that survive the process boundary, and the status code each
// travels as. Anything not listed arrives as itself and nothing more, which is
// the right answer: a plugin's own failure is the plugin's to describe.
var sentinelCodes = []struct {
	err  error
	code codes.Code
}{
	{ErrInvalid, codes.InvalidArgument},
	{ErrUnsupported, codes.Unimplemented},
	{ErrNoAnswer, codes.NotFound},
	{ErrNotConfigured, codes.FailedPrecondition},
	{ErrNotAllowed, codes.PermissionDenied},
	{ErrUnavailable, codes.Unavailable},
}

// toStatus turns an error into one the other process can branch on.
func toStatus(err error) error {
	if err == nil {
		return nil
	}
	for _, s := range sentinelCodes {
		if errors.Is(err, s.err) {
			return status.Error(s.code, err.Error())
		}
	}
	return status.Error(codes.Unknown, err.Error())
}

// fromStatus turns it back. A deadline or a dead process arrives as the
// context or transport error it is, which is what the caller's own handling is
// written against.
func fromStatus(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	switch st.Code() {
	case codes.DeadlineExceeded:
		return fmt.Errorf("%w: %s", context.DeadlineExceeded, st.Message())
	case codes.Canceled:
		return fmt.Errorf("%w: %s", context.Canceled, st.Message())
	default:
		// Everything else is either one of the sentinels below or the other
		// side's own failure, which travels as itself.
	}
	for _, s := range sentinelCodes {
		if st.Code() == s.code {
			return &remote{sentinel: s.err, message: st.Message()}
		}
	}
	return err
}

// orEmptyObject and orEmptyArray let a peer send nothing where a document was
// expected. A plugin in another language that leaves a field unset is not
// broken, and refusing it would be this side being fussy about JSON rather than
// about meaning.
func orEmptyObject(b []byte) []byte {
	if len(b) == 0 {
		return []byte("{}")
	}
	return b
}

func orEmptyArray(b []byte) []byte {
	if len(b) == 0 {
		return []byte("[]")
	}
	return b
}

// Serve is the request half of a plugin's own route.
//
// The plugin's implementation is optional, so this is where a manifest that
// asked for a route and a binary that cannot serve one meet: the answer is
// "unimplemented", which the host turns into a plugin that is refused rather
// than a page that 500s.
func (s *server) Serve(ctx context.Context, in *pb.ServeRequest) (*pb.ServeResponse, error) {
	impl, ok := s.impl.(Server)
	if !ok {
		return nil, status.Error(codes.Unimplemented,
			"this plugin declared a route in its manifest and does not serve one")
	}

	r := HTTPRequest{
		Method:  in.GetMethod(),
		Path:    in.GetPath(),
		Query:   in.GetQuery(),
		Header:  headerFrom(in.GetHeaders()),
		Body:    in.GetBody(),
		Prefix:  in.GetPrefix(),
		BaseURL: in.GetBaseUrl(),
		Secrets: secretsFrom(in.GetSecrets()),
	}
	if c := in.GetCaller(); c != nil {
		r.Caller = Caller{
			DriverSlug: c.GetDriverSlug(),
			DriverName: c.GetDriverName(),
			AdminEmail: c.GetAdminEmail(),
			Remote:     c.GetRemote(),
		}
	}
	if err := json.Unmarshal(orEmptyObject(in.GetValuesJson()), &r.Settings); err != nil {
		return nil, toStatus(fmt.Errorf("%w: the settings are not readable: %w", ErrInvalid, err))
	}

	res, err := impl.ServeHTTP(ctx, r)
	if err != nil {
		return nil, toStatus(err)
	}
	if bad := res.Usage.Validate(); bad != nil {
		return nil, toStatus(bad)
	}
	usage, err := json.Marshal(res.Usage)
	if err != nil {
		return nil, toStatus(fmt.Errorf("%w: what that request cost cannot be encoded: %w", ErrInvalid, err))
	}
	return &pb.ServeResponse{
		Status:    uint32(max(res.Status, 0)), //nolint:gosec // G115: a status the host checks is in range before it writes it.
		Headers:   headersTo(res.Header),
		Body:      res.Body,
		SignIn:    res.SignIn,
		SignOut:   res.SignOut,
		UsageJson: usage,
	}, nil
}

// ServeHTTP is the host's side of the same call. It is named to satisfy
// [Server], because that is how the host asks whether a plugin serves at all:
// the interface is the check.
func (c *client) ServeHTTP(ctx context.Context, r HTTPRequest) (HTTPResponse, error) {
	values, err := json.Marshal(r.Settings)
	if err != nil {
		return HTTPResponse{}, fmt.Errorf("%w: the settings cannot be encoded: %w", ErrInvalid, err)
	}
	out, err := c.c.Serve(ctx, &pb.ServeRequest{
		Method:  r.Method,
		Path:    r.Path,
		Query:   r.Query,
		Headers: headersTo(r.Header),
		Body:    r.Body,
		Caller: &pb.Caller{
			DriverSlug: r.Caller.DriverSlug,
			DriverName: r.Caller.DriverName,
			AdminEmail: r.Caller.AdminEmail,
			Remote:     r.Caller.Remote,
		},
		Prefix:     r.Prefix,
		BaseUrl:    r.BaseURL,
		ValuesJson: values,
		Secrets:    r.Secrets.raw(),
	})
	if err != nil {
		return HTTPResponse{}, fromStatus(err)
	}
	res := HTTPResponse{
		Status:  int(out.GetStatus()),
		Header:  headerFrom(out.GetHeaders()),
		Body:    out.GetBody(),
		SignIn:  out.GetSignIn(),
		SignOut: out.GetSignOut(),
	}
	if err := json.Unmarshal(orEmptyObject(out.GetUsageJson()), &res.Usage); err != nil {
		return HTTPResponse{}, fmt.Errorf("%w: what that request cost is not readable: %w", ErrInvalid, err)
	}
	return res, nil
}

// headersTo and headerFrom carry an http.Header across the wire. Protobuf maps
// cannot hold a repeated field, so each header's values travel in a message of
// their own; a header with no values is dropped rather than sent as an empty
// one, because the two mean the same thing and only one of them survives a
// round trip.
func headersTo(h http.Header) map[string]*pb.HeaderValues {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string]*pb.HeaderValues, len(h))
	for name, values := range h {
		if len(values) == 0 {
			continue
		}
		out[name] = &pb.HeaderValues{Values: values}
	}
	return out
}

func headerFrom(m map[string]*pb.HeaderValues) http.Header {
	if len(m) == 0 {
		return nil
	}
	out := make(http.Header, len(m))
	for name, values := range m {
		if v := values.GetValues(); len(v) > 0 {
			out[http.CanonicalHeaderKey(name)] = v
		}
	}
	return out
}
