package faf

import (
	"context"
	"errors"
	"faf-pioneer/applog"
	"faf-pioneer/gpgnet"
	"faf-pioneer/launcher"
	"faf-pioneer/util"
	"fmt"
	"go.uber.org/zap"
	"net"
)

type GpgNetLauncherServer struct {
	ctx                  context.Context
	port                 uint
	tcpListener          net.Listener
	gameState            gpgnet.GameState
	info                 *launcher.Info
	fafClientFromAdapter chan<- gpgnet.Message
	fafClientToAdapter   chan gpgnet.Message
	currentClient        *GpgNetLauncherClient
}

func NewGpgNetLauncherServer(context context.Context, info *launcher.Info, port uint) *GpgNetLauncherServer {
	return &GpgNetLauncherServer{
		ctx:       context,
		port:      port,
		gameState: gpgnet.GameStateNone,
		info:      info,
	}
}

func (s *GpgNetLauncherServer) Listen(
	fafClientToAdapter chan gpgnet.Message,
	fafClientFromAdapter chan<- gpgnet.Message,
	adapterConnectedCallback func(),
) error {
	lc := net.ListenConfig{}
	listener, err := lc.Listen(s.ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %v", s.port, err)
	}

	defer func(listener net.Listener) {
		_ = listener.Close()
	}(listener)

	s.ctx = applog.AddContextFields(s.ctx,
		zap.Uint("listenPort", s.port),
	)

	applog.FromContext(s.ctx).Info("Listening GPG-Net launcher server")

	s.tcpListener = listener
	s.fafClientToAdapter = fafClientToAdapter
	s.fafClientFromAdapter = fafClientFromAdapter

	for {
		conn, acceptErr := util.NetAcceptWithContext(s.ctx, listener)
		if acceptErr != nil {
			if errors.Is(acceptErr, net.ErrClosed) {
				return nil
			}

			if s.ctx.Err() != nil {
				applog.FromContext(s.ctx).Debug(
					"Context canceled, stopping accepting launcher server connections",
				)
				return nil
			}

			applog.FromContext(s.ctx).Error(
				"Failed to accept new GPG-Net adapter connection",
				zap.Error(acceptErr),
			)
			continue
		}

		if s.currentClient != nil {
			_ = s.currentClient.Close()
		}

		s.currentClient = s.acceptConnection(conn)
		adapterConnectedCallback()
	}
}

func (s *GpgNetLauncherServer) acceptConnection(conn net.Conn) *GpgNetLauncherClient {
	ctx := applog.AddContextFields(
		s.ctx,
		zap.String("remoteAddr", conn.RemoteAddr().String()),
	)

	client := &GpgNetLauncherClient{
		ctx:                  ctx,
		connection:           conn,
		server:               s,
		fafClientToAdapter:   s.fafClientToAdapter,
		fafClientFromAdapter: s.fafClientFromAdapter,
	}

	applog.FromContext(ctx).Info("Adapter connected to the launcher server")

	client.listen(conn)
	return client
}

func (s *GpgNetLauncherServer) Close() error {
	if s.currentClient != nil {
		return s.currentClient.Close()
	}

	err := s.tcpListener.Close()
	if err != nil {
		applog.FromContext(s.ctx).Error(
			"Failed to close launcher server listener",
			zap.Error(err),
		)
	}

	return err
}

func (s *GpgNetLauncherServer) SendMessagesToGame(messages ...gpgnet.Message) {
	for _, msg := range messages {
		s.currentClient.sendMessage(msg)
	}
}

func (s *GpgNetLauncherServer) setGameState(state gpgnet.GameState) {
	s.gameState = state
}

func (s *GpgNetLauncherServer) GetGameState() gpgnet.GameState {
	return s.gameState
}
