package main

import (
	"bufio"
	"faf-pioneer/applog"
	"faf-pioneer/faf"
	"faf-pioneer/gpgnet"
	"faf-pioneer/launcher"
	"faf-pioneer/util"
	"fmt"
	"go.uber.org/zap"
	"golang.org/x/net/context"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	defer cancel()

	info := launcher.NewInfoFromFlags()
	applog.Initialize(info.UserId, info.GameId)
	defer applog.Shutdown()
	defer util.WrapAppContextCancelExitMessage(ctx, "Launcher-emulator")

	if err := info.Validate(); err != nil {
		applog.Fatal("Failed to validate command line arguments", zap.Error(err))
		return
	}

	applog.LogStartup(info)

	// Client starts an own GPG-Net server that used to communicate between FAF-Client and FAF.exe.
	// So for that we need to create GpgNetServer and start listening on gpgNetClientPort.

	adapterToFafClient := make(chan gpgnet.Message)
	fafClientToAdapter := make(chan gpgnet.Message)

	server := faf.NewGpgNetLauncherServer(ctx, info, info.GpgNetClientPort)
	fafProcess := NewGameProcess(ctx, info)

	adapterConnected := func() {
		if err := fafProcess.Start(); err != nil {
			applog.Error("Failed to start game process", zap.Error(err))
			return
		}

		// Let's wait for process to exit in another routine,
		// so we can do graceful shutdown for launcher emulator process.
		go func() {
			_ = fafProcess.cmd.Wait()
			cancel()
		}()
	}

	go func() {
		err := server.Listen(adapterToFafClient, fafClientToAdapter, adapterConnected)
		if err != nil {
			applog.Fatal("Failed to connect to GPG-Net server", zap.Error(err))
		}
	}()

	cr := util.NewCancelableIoReader(ctx, os.Stdin)
	scanner := bufio.NewScanner(cr)

	// How to test
	// - For Host (UserA) start faf-launcher-emulator and then faf-adapter.
	// - For Host (UserA) type in faf-launcher-emulator a command:
	//   > host.
	// Then another player should join as:
	// - For Host (UserB) start faf-launcher-emulator and then faf-adapter.
	// - For Host (UserB) type in faf-launcher-emulator a command:
	//   > `join_to UserA 1 <gameToWebrtcPort of UserA>`
	// - For Host (UserA) type in faf-launcher-emulator a command:
	//   > `connect_to UserB 2 <gameToWebrtcPort of UserB>`

	for scanner.Scan() {
		value := scanner.Text()
		applog.Debug("Entered command", zap.String("rawCommand", value))

		if strings.HasPrefix(value, "host") {
			applog.Info("Sending host game messages to the adapter/game")

			if server.GetGameState() != gpgnet.GameStateIde {
				applog.Warn("Game is not in Idle state yet, wait and retry")
				continue
			}

			server.SendMessagesToGame(
				gpgnet.NewHostGameMessage(""),
			)
			continue
		}

		if strings.HasPrefix(value, "join_to") {
			applog.Info("First joining stage sending JoinGameMessage")

			args := strings.Split(value, " ")[1:]
			user := args[0]
			uid, _ := strconv.Atoi(args[1])
			port, _ := strconv.Atoi(args[2])

			server.SendMessagesToGame(
				gpgnet.NewJoinGameMessage(
					user,
					int32(uid),
					fmt.Sprintf("127.0.0.1:%d", port),
				),
			)
			continue
		}

		if strings.HasPrefix(value, "connect_to") {
			applog.Info("Sending join game messages to the adapter/game")

			// connect_to UserA 1 14080
			// connect_to UserB 2 14081

			args := strings.Split(value, " ")[1:]
			user := args[0]
			uid, _ := strconv.Atoi(args[1])
			port, _ := strconv.Atoi(args[2])

			server.SendMessagesToGame(
				gpgnet.NewConnectToPeerMessage(
					user,
					int32(uid),
					fmt.Sprintf("127.0.0.1:%d", int32(port)),
				),
			)

			continue
		}

		if strings.HasPrefix(value, "quit") {
			_ = server.Close()
			return
		}
	}
}
