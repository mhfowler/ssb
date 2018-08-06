package gossip

import (
	"context"
	"fmt"
	"net"
	"runtime/debug"
	"strings"
	"time"

	"github.com/cryptix/go/logging"
	"github.com/pkg/errors"
	"go.cryptoscope.co/librarian"
	"go.cryptoscope.co/margaret/multilog"
	"go.cryptoscope.co/muxrpc"
	"go.cryptoscope.co/netwrap"
	"go.cryptoscope.co/sbot"
	"go.cryptoscope.co/secretstream"
)

type handler struct {
	Node sbot.Node
	Repo sbot.Repo
	Info logging.Interface

	Promisc bool
}

func (g *handler) HandleConnect(ctx context.Context, e muxrpc.Endpoint) {
	srv := e.(muxrpc.Server)
	g.Info.Log("event", "onConnect", "handler", "gossip", "addr", srv.Remote())

	shsID, ok := netwrap.GetAddr(srv.Remote(), "shs-bs").(secretstream.Addr)
	if !ok {
		return
	}

	ref, err := sbot.ParseRef(shsID.String())
	if err != nil {
		g.Info.Log("handleConnect", "sbot.ParseRef", "err", err)
		return
	}

	// fetch calling feed
	fref, ok := ref.(*sbot.FeedRef)
	if !ok {
		g.Info.Log("handleConnect", "notFeedRef", "r", shsID.String())
		return
	}

	if err := g.fetchFeed(ctx, *fref, e); err != nil {
		g.Info.Log("handleConnect", "fetchFeed remote failed", "r", fref.Ref(), "err", err)
		return
	}
	g.Info.Log("fetchFeed", "done calle", "ref", fref.Ref())

	userFeeds := g.Repo.UserFeeds()
	mykp := g.Repo.KeyPair()
	hasOwn, err := multilog.Has(userFeeds, librarian.Addr(mykp.Id.ID))
	if err != nil {
		g.Info.Log("handleConnect", "multilog.Has(userFeeds,myID)", "err", err)
		return
	}

	if !hasOwn {
		g.Info.Log("handleConnect", "oops - dont have my own feed. requesting")
		if err := g.fetchFeed(ctx, mykp.Id, e); err != nil {
			g.Info.Log("handleConnect", "my fetchFeed failed", "r", mykp.Id.Ref(), "err", err)
			return
		}
		g.Info.Log("fetchFeed", "done self")
	}

	ufaddrs, err := userFeeds.List()
	if err != nil {
		g.Info.Log("handleConnect", "userFeeds listing failed", "err", err)
		return
	}
	for i, addr := range ufaddrs {
		userRef := sbot.FeedRef{
			Algo: "ed25519",
			ID:   []byte(addr),
		}
		err = g.fetchFeed(ctx, userRef, e)
		if err != nil {
			g.Info.Log("handleConnect", "fetchFeed stored failed", "err", err, "i", i)
			return
		}
		g.Info.Log("fetchFeed", "done list", "ref", userRef.Ref())
	}
}

func (g *handler) check(err error) {
	if err != nil {
		g.Info.Log("error", err)
		debug.PrintStack()
	}
}

func (g *handler) HandleCall(ctx context.Context, req *muxrpc.Request, edp muxrpc.Endpoint) {
	// g.Info.Log("event", "onCall", "handler", "gossip", "args", fmt.Sprintf("%v", req.Args), "method", req.Method)
	if req.Type == "" {
		req.Type = "async"
	}

	var closed bool
	checkAndClose := func(err error) {
		g.check(err)
		if err != nil {
			closed = true
			closeErr := req.Stream.CloseWithError(err)
			g.check(errors.Wrapf(closeErr, "error closeing request. %s", req.Method))
		}
	}

	defer func() {
		if !closed {
			g.check(errors.Wrapf(req.Stream.Close(), "gossip: error closing call: %s", req.Method))
		}
	}()

	switch req.Method.String() {

	case "createHistoryStream":
		if req.Type != "source" {
			checkAndClose(errors.Errorf("createHistoryStream: wrong tipe. %s", req.Type))
			return
		}
		if err := g.pourFeed(ctx, req); err != nil {
			checkAndClose(errors.Wrap(err, "createHistoryStream failed"))
			return
		}
		return

	case "gossip.ping":
		if err := g.ping(ctx, req); err != nil {
			checkAndClose(errors.Wrap(err, "gossip.ping failed."))
			return
		}

	case "gossip.connect":
		if len(req.Args) != 1 {
			// TODO: use secretstream
			g.Info.Log("error", "usage", "args", req.Args, "method", req.Method)
			checkAndClose(errors.New("usage: gossip.connect host:port:key"))
			return
		}
		destString, ok := req.Args[0].(string)
		if !ok {
			err := errors.Errorf("gossip.connect call: expected argument to be string, got %T", req.Args[0])
			checkAndClose(err)
			return
		}
		if err := g.connect(ctx, destString); err != nil {
			checkAndClose(errors.Wrap(err, "gossip.connect failed."))
			return
		}
		g.check(req.Return(ctx, "connected"))

	default:
		checkAndClose(errors.Errorf("unknown command: %s", req.Method))
	}
}

func (g *handler) ping(ctx context.Context, req *muxrpc.Request) error {
	g.Info.Log("event", "ping", "args", fmt.Sprintf("%v", req.Args))
	for i := 0; i < 2; i++ {
		err := req.Stream.Pour(ctx, time.Now().Unix())
		if err != nil {
			return errors.Wrapf(err, "pour(%d) failed to pong", i)
		}
		time.Sleep(time.Second)
	}
	return req.Stream.CloseWithError(errors.New("TODO:dos0day"))
}

func (g *handler) connect(ctx context.Context, dest string) error {
	splitted := strings.Split(dest, ":")
	if n := len(splitted); n != 3 {
		return errors.Errorf("gossip.connect: bad request. expected 3 parts, got %d", n)
	}

	addr, err := net.ResolveTCPAddr("tcp", strings.Join(splitted[:2], ":"))
	if err != nil {
		return errors.Wrapf(err, "gossip.connect call: error resolving network address %q", splitted[:2])
	}

	ref, err := sbot.ParseRef(splitted[2])
	if err != nil {
		return errors.Wrapf(err, "gossip.connect call: failed to parse FeedRef %s", splitted[2])
	}

	remoteFeed, ok := ref.(*sbot.FeedRef)
	if !ok {
		return errors.Errorf("gossip.connect: expected FeedRef got %T", ref)
	}

	wrappedAddr := netwrap.WrapAddr(addr, secretstream.Addr{PubKey: remoteFeed.ID})
	g.Info.Log("event", "doing gossip.connect", "remote", wrappedAddr.String())
	err = g.Node.Connect(ctx, wrappedAddr)
	return errors.Wrapf(err, "gossip.connect call: error connecting to %q", addr)
}