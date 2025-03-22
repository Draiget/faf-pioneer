package faf

import (
	"context"
	"faf-pioneer/applog"
	"faf-pioneer/gpgnet"
	"faf-pioneer/launcher"
	"faf-pioneer/util"
	"fmt"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"net"
)

type GpgNetLauncherServer struct {
	ctx                  context.Context
	port                 uint
	tcpListener          net.Listener
	gameState            gpgnet.GameState
	loggerFields         []zap.Field
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

	applog.Info("Listening GPG-Net launcher server", zap.Uint("port", s.port))

	s.tcpListener = listener
	s.fafClientToAdapter = fafClientToAdapter
	s.fafClientFromAdapter = fafClientFromAdapter

	for {
		conn, acceptErr := util.NetAcceptWithContext(s.ctx, listener)
		if acceptErr != nil {
			if s.ctx.Err() != nil {
				applog.Debug("Context canceled, stopping accepting launcher server connections")
				return nil
			}

			applog.Error("Failed to accept new GPG-Net adapter connection", zap.Error(err))
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
	s.loggerFields = []zapcore.Field{
		zap.Uint("listenPort", s.port),
		zap.String("remoteAddr", conn.RemoteAddr().String()),
	}

	client := &GpgNetLauncherClient{
		ctx:                  s.ctx,
		connection:           conn,
		server:               s,
		loggerFields:         s.loggerFields,
		fafClientToAdapter:   s.fafClientToAdapter,
		fafClientFromAdapter: s.fafClientFromAdapter,
	}

	applog.Info("Adapter connected to the launcher server", s.loggerFields...)

	client.listen(conn)
	return client
}

func (s *GpgNetLauncherServer) Close() error {
	if s.currentClient != nil {
		return s.currentClient.Close()
	}

	err := s.tcpListener.Close()
	if err != nil {
		applog.Error(
			"Failed to close launcher server listener",
			append(s.loggerFields, zap.Error(err))...,
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
