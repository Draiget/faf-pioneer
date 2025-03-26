package util

import (
	"context"
	"faf-pioneer/applog"
	"faf-pioneer/gpgnet"
	"fmt"
	"go.uber.org/zap"
	"net"
	"strings"
)

func PtrValueOrDef[T any](value *T, def T) T {
	if value == nil {
		return def
	}
	return *value
}

func WrapAppContextCancelExitMessage(ctx context.Context, appName string) {
	ctxErr := ctx.Err()
	if ctxErr != nil {
		applog.Info(fmt.Sprintf("%s exited; context cancelled", appName), zap.Error(ctxErr))
		return
	}

	applog.Info(fmt.Sprintf("%s exited", appName))
}

func DataToHex(buffer []byte) string {
	var parts []string
	for _, b := range buffer {
		parts = append(parts, fmt.Sprintf("%02X", b))
	}
	return strings.Join(parts, " ")
}

type DumpDirection = uint8

const (
	DumpDirectionFromPeer DumpDirection = iota
	DumpDirectionToGame
)

func DumpPacket(buffer []byte, addr *net.UDPAddr, msg string, dir DumpDirection) {
	var fields []zap.Field
	if dir == DumpDirectionFromPeer {
		fields = append(fields, zap.String("receivedFrom", addr.String()))
	} else {
		fields = append(fields, zap.String("sentTo", addr.String()))
	}

	data, err := gpgnet.ParseGamePacket(buffer)
	if err != nil {
		// TODO: Decompression set on CONNECT packet, so we need to parse packets to see if
		//       host chosen a compression, no reason to try decompress every packet.
		// decompressed, err := DeflateDecompressData(data.Payload)
		// if err == nil {
		// 	applog.Debug("UDP proxy data received from game (decompressed)",
		// 		zap.String("receivedFrom", addr.String()),
		// 		zap.String("data", DataToHex(decompressed)))
		// 	return
		// }
		applog.Debug(fmt.Sprintf("%s (unparsable)", msg),
			append(fields,
				zap.String("data", DataToHex(buffer)),
			)...,
		)
		return
	}

	applog.Debug(msg,
		append(fields,
			zap.Uint16("seq", data.Header.Sequence),
			zap.Uint16("ackSeq", data.Header.AckSequence),
			zap.Uint32("type", data.Header.Type),
			zap.Uint16("payloadLength", data.Header.PayloadLength),
			zap.String("extraHeader", DataToHex(data.Header.Extra[:])),
			zap.String("payload", DataToHex(data.Payload)),
		)...)
}
