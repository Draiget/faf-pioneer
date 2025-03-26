package gpgnet

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"time"
)

type PacketType = uint32

const (
	PacketTypeConnect      PacketType = 0x00000000 // CONNECT
	PacketTypeAnswer       PacketType = 0x00000001 // ANSWER
	PacketTypeResetSerial  PacketType = 0x00000002 // RESETSERIAL
	PacketTypeSerialReset  PacketType = 0x00000003 // SERIALRESET
	PacketTypeData         PacketType = 0x00000004 // DATA
	PacketTypeAck          PacketType = 0x00000005 // ACK
	PacketTypeKeepalive    PacketType = 0x00000006 // KEEPALIVE
	PacketTypeGoodbye      PacketType = 0x00000007 // GOODBYE
	PacketTypeNatTraversal PacketType = 0x00000008 // NATTRAVERSAL
)

// PacketHeader is a game network packet description with type, payload length, sequence and more.
// Had size of 15 bytes.
type PacketHeader struct {
	Type          PacketType // (0x00) 4 bytes: type of the packet.
	UnknownFlag   byte       // (0x04) 1 byte: unknown flag.
	Sequence      uint16     // (0x05) 2 byte: sequence number of a packet.
	AckSequence   uint16     // (0x07) 2 byte: sequence number that we're acked (answer to a seq. number)
	Extra         [4]byte    // (0x09) 4 bytes: extra data?
	PayloadLength uint16     // (0x13) 2 bytes: payload length.
}

// GamePacket is main game network packet structure with a payload data.
type GamePacket struct {
	Header  PacketHeader // (0x00) 15 bytes: packet header information.
	Payload []byte       // (0x15): `Header.PayloadLength` bytes, our payload.
}

type ConnectPacketPayload struct {
	Protocol uint32 // (0x00) 4 bytes: number of connection protocol (currently 2 is supported)
}

// GamePacketContainer – container used by game within `recvfrom` (sub_128BBF0).
// Layout is the following:
//   - 0x00–0x07: Unknown1 (container[0] and container[1])
//   - 0x08–0x0F: ReceivedTime (container[2] and container[3])
//   - 0x10–0x13: Unknown2 (container[4])
//   - 0x14–0x17: ReceivedBytes (container[5])
//   - 0x24-....: Packet (container[6])
type GamePacketContainer struct {
	Unknown1      [8]byte    // (0x00): unknown.
	ReceivedTime  int64      // (0x08): low and high 32 bits of receive packet time.
	Unknown2      [4]byte    // (0x10): unknown.
	ReceivedBytes int32      // (0x14): raw amount of bytes received by `recvfrom`.
	Packet        GamePacket // (0x24): packet itself.
}

type ConnectionState int32

const (
	StatePending ConnectionState = iota
	StateConnecting
	StateAnswering
	StateEstablished
	StateTimedOut
	StateErrored
)

type UDPConnection struct {
	ExpectedSeq         uint8           // Next expected packet sequence number.
	State               ConnectionState // Current connection state.
	UpdateField1        uint32          // Original offset +1088 in connection object.
	UpdateField2        uint32          // Original offset +1092 in connection object.
	LastAnswerTimestamp uint64          // Last timestamp received in `ANSWER` packet.
	LastKeepAlive       time.Time
	LastReceived        time.Time
	LastSend            time.Time
}

func ParseGamePacket(data []byte) (*GamePacket, error) {
	if len(data) < 15 {
		return nil, fmt.Errorf("packet too short: %d bytes", len(data))
	}
	hdr := PacketHeader{
		Type:          binary.LittleEndian.Uint32(data[0:4]),
		Sequence:      binary.LittleEndian.Uint16(data[5:7]),
		AckSequence:   binary.LittleEndian.Uint16(data[7:9]),
		PayloadLength: binary.LittleEndian.Uint16(data[13:15]),
	}

	copy(hdr.Extra[:], data[9:13])
	if len(data) != int(15+hdr.PayloadLength) {
		return nil, fmt.Errorf(
			"payload length mismatch: got %d, header says %d",
			len(data)-15,
			hdr.PayloadLength)
	}

	return &GamePacket{
		Header:  hdr,
		Payload: data[15:],
	}, nil
}

func ProcessPacket(packet *GamePacket, conn *UDPConnection, srcAddr net.Addr) {
	log.Printf("Received packet from %s: Type=0x%08X, Seq=%d, PayloadLength=%d, Payload=% X",
		srcAddr, packet.Header.Type, packet.Header.Sequence, packet.Header.PayloadLength, packet.Payload)

	switch packet.Header.Type {
	case PacketTypeConnect:
		log.Println("Processing CONNECT packet")
	case PacketTypeAnswer:
		processAnswer(packet, conn)
	case PacketTypeResetSerial:
		log.Println("Processing RESETSERIAL packet")
	case PacketTypeSerialReset:
		log.Println("Processing SERIALRESET packet")
	case PacketTypeData:
		processData(packet, conn)
	case PacketTypeAck:
		processAck(packet, conn)
	case PacketTypeKeepalive:
		processKeepalive(packet, conn)
	case PacketTypeGoodbye:
		processGoodbye(packet, conn)
	case PacketTypeNatTraversal:
		processNatTraversal(packet, conn)
	default:
		log.Printf("Ignoring unknown packet type: 0x%08X", packet.Header.Type)
	}
}

func processAnswer(packet *GamePacket, conn *UDPConnection) {
	log.Printf("Processing ANSWER packet: Seq=%d, Payload=% X", packet.Header.Sequence, packet.Payload)
	conn.LastReceived = time.Now()
	if len(packet.Payload) >= 8 {
		conn.UpdateField1 = binary.LittleEndian.Uint32(packet.Payload[0:4])
		conn.UpdateField2 = binary.LittleEndian.Uint32(packet.Payload[4:8])
		log.Printf("Updated connection fields: 0x%X, 0x%X", conn.UpdateField1, conn.UpdateField2)
	}
}

func processData(packet *GamePacket, conn *UDPConnection) {
	delta := int(packet.Header.Sequence) - int(conn.ExpectedSeq)
	if delta < 0 {
		log.Printf("Ignoring repeat of old DATA (Seq=%d, Expected=%d)",
			packet.Header.Sequence,
			conn.ExpectedSeq)
		return
	} else if delta > 32 {
		log.Printf("Ignoring DATA from too far in the future (Seq=%d, Expected=%d, Delta=%d)",
			packet.Header.Sequence,
			conn.ExpectedSeq, delta)
		return
	}

	log.Printf("Processing DATA packet: Seq=%d, Payload=% X", packet.Header.Sequence, packet.Payload)
	conn.ExpectedSeq++
	conn.LastReceived = time.Now()
}

func processAck(packet *GamePacket, conn *UDPConnection) {
	switch conn.State {
	case StatePending:
		log.Printf("CNetUDPConnection<%d,%s>::ProcessAck(): ignoring traffic on Pending connection", 0, "?")
		return
	case StateConnecting:
		log.Printf("CNetUDPConnection<%d,%s>::ProcessAck(): ignoring traffic on Connecting connection", 0, "?")
		return
	case StateErrored:
		log.Printf("CNetUDPConnection<%d,%s>::ProcessAck(): ignoring traffic on Errored connection", 0, "?")
		return
	default:
	}

	log.Printf("Processing ACK packet: Seq=%d", packet.Header.Sequence)
	conn.LastReceived = time.Now()
}

func processKeepalive(packet *GamePacket, conn *UDPConnection) {
	log.Printf("Processing KEEPALIVE packet: Seq=%d", packet.Header.Sequence)
	conn.LastKeepAlive = time.Now()
}

func processGoodbye(packet *GamePacket, conn *UDPConnection) {
	log.Printf("Processing GOODBYE packet: Seq=%d", packet.Header.Sequence)
}

func processNatTraversal(packet *GamePacket, conn *UDPConnection) {
	log.Printf("Processing NATTRAVERSAL packet: Seq=%d", packet.Header.Sequence)
}
