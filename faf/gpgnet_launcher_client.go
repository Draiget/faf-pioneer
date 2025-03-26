package faf

import (
	"bufio"
	"context"
	"errors"
	"faf-pioneer/applog"
	"faf-pioneer/gpgnet"
	"go.uber.org/zap"
	"io"
	"net"
)

type GpgNetLauncherClient struct {
	ctx                  context.Context
	connection           net.Conn
	connCancel           context.CancelFunc
	server               *GpgNetLauncherServer
	fafClientFromAdapter chan<- gpgnet.Message
	fafClientToAdapter   chan gpgnet.Message
}

func (s *GpgNetLauncherClient) listen(conn net.Conn) {
	clientCtx, cancel := context.WithCancel(s.ctx)
	s.connCancel = cancel

	// Wrap the connection in a buffered reader.
	bufferReader := bufio.NewReader(conn)
	faStreamReader := NewFaStreamReader(bufferReader)

	// Wrap second goroutine with GPG-Net messages forwarder to game.
	bufferedWriter := bufio.NewWriter(conn)
	faStreamWriter := NewFaStreamWriter(bufferedWriter)

	go s.handleFromAdapter(clientCtx, faStreamReader)
	go s.handleToAdapter(clientCtx, faStreamWriter)
}

func (s *GpgNetLauncherClient) handleFromAdapter(ctx context.Context, stream *StreamReader) {
	applog.FromContext(s.ctx).Info("Waiting for incoming GPG-Net messages from adapter")

	// Read one message from the connection, process it and continue reading.
	for {
		// First, read length-prefixed string from the stream to determine chunks size.
		command, err := stream.ReadString()
		if errors.Is(err, net.ErrClosed) {
			applog.FromContext(s.ctx).Info("Closing GPG-Net connection from adapter (remotely closed)")
			_ = s.Close()
			return
		}

		if errors.Is(err, io.EOF) {
			applog.FromContext(s.ctx).Info("Closing GPG-Net connection from adapter (EOF reached)")
			_ = s.Close()
			return
		}

		if err != nil {
			applog.FromContext(s.ctx).Error(
				"Error parsing GPG-Net command from adapter, closing connection",
				zap.Error(err),
			)
			_ = s.Close()
			return
		}

		select {
		case <-ctx.Done():
			applog.FromContext(s.ctx).Debug("Context canceled in handleFromAdapter, stopping read loop")
			_ = s.Close()
			return
		default:
		}

		// Then, read the "chunks" (actual message data).
		chunks, err := stream.ReadChunks()
		if errors.Is(err, io.EOF) {
			applog.FromContext(s.ctx).Info("Closing GPG-Net connection from adapter (EOF reached)")
			_ = s.Close()
			return
		}
		if err != nil {
			applog.FromContext(s.ctx).Error(
				"Error parsing GPG-Net command chunks from adapter, closing connection",
				zap.Error(err),
			)
			_ = s.Close()
			return
		}

		select {
		case <-ctx.Done():
			applog.FromContext(s.ctx).Debug("Context canceled in handleFromAdapter, stopping read loop")
			_ = s.Close()
			return
		default:
		}

		unparsedMsg := gpgnet.BaseMessage{
			Command: command,
			Args:    chunks,
		}

		// Try to parse GPG-Net message based on the command type/name.
		parsedMsg, err := unparsedMsg.TryParse()
		if err != nil {
			applog.FromContext(s.ctx).Error(
				"Failed to parse GPG-Net message from adapter",
				zap.Error(err),
			)
			// TODO: Forward unparsed?
		}

		// Process parsed GPG-Net command.
		parsedMsg = s.processMessage(parsedMsg)
		if parsedMsg != nil {
			s.fafClientFromAdapter <- parsedMsg
		}
	}
}

func (s *GpgNetLauncherClient) handleToAdapter(ctx context.Context, stream *StreamWriter) {
	applog.FromContext(s.ctx).Info("Waiting for GPG-Net messages from game to be forwarded to the adapter")

	for {
		select {
		case msg, ok := <-s.fafClientToAdapter:
			if !ok {
				applog.FromContext(s.ctx).Debug(
					"Channel (fafClientToAdapter) closed, GpgNetLauncherClient::handleToAdapter aborted",
				)
				_ = s.Close()
				return
			}

			applog.FromContext(s.ctx).Debug(
				"Forwarding GPG-Net message in launcher client from (fafClientToAdapter) to the adapter",
				zap.String("command", msg.GetCommand()),
			)

			err := stream.WriteMessage(msg)
			if errors.Is(err, net.ErrClosed) {
				applog.FromContext(s.ctx).Error(
					"Failed to write GPG-Net message to the adapter, connection was closed",
					zap.Error(err),
				)
				_ = s.Close()
				return
			}

			if err != nil {
				applog.FromContext(s.ctx).Error("Failed to write GPG-Net message to the adapter", zap.Error(err))
			}
			if err = stream.w.Flush(); err != nil {
				applog.FromContext(s.ctx).Error("Failed to flush GPG-Net message to game", zap.Error(err))
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *GpgNetLauncherClient) processMessage(rawMessage gpgnet.Message) gpgnet.Message {
	switch msg := rawMessage.(type) {
	case *gpgnet.GameStateMessage:
		applog.FromContext(s.ctx).Info(
			"Game state has been changed",
			zap.String("gameState", msg.State),
		)

		s.server.setGameState(msg.State)

		switch msg.State {
		case gpgnet.GameStateIde:
			// TODO: Player service emulation to get userId & userName?

			s.sendMessage(gpgnet.NewCreateLobbyMessage(
				gpgnet.LobbyInitModeNormal,
				int32(0),
				s.server.info.UserName,
				int32(s.server.info.UserId),
			))

		case gpgnet.GameStateLobby:
		}

		break
	case *gpgnet.GameFullMessage:
		applog.FromContext(s.ctx).Info("Received GameFullMessage")
		break
	default:
		applog.FromContext(s.ctx).Debug(
			"Message command ignored",
			zap.String("command", msg.GetCommand()),
		)
	}

	return rawMessage
}

func (s *GpgNetLauncherClient) sendMessage(message gpgnet.Message) {
	s.fafClientToAdapter <- message
}

func (s *GpgNetLauncherClient) Close() error {
	s.connCancel()
	return s.connection.Close()
}
