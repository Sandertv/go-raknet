package message

const (
	IDConnectedPing                  byte = 0x00
	IDUnconnectedPing                byte = 0x01
	IDUnconnectedPingOpenConnections byte = 0x02
	IDConnectedPong                  byte = 0x03
	IDDetectLostConnections          byte = 0x04
	IDOpenConnectionRequest1         byte = 0x05
	IDOpenConnectionReply1           byte = 0x06
	IDOpenConnectionRequest2         byte = 0x07
	IDOpenConnectionReply2           byte = 0x08
	IDConnectionRequest              byte = 0x09
	IDConnectionRequestAccepted      byte = 0x10
	IDNewIncomingConnection          byte = 0x13
	IDDisconnectNotification         byte = 0x15

	IDIncompatibleProtocolVersion byte = 0x19

	IDUnconnectedPong byte = 0x1c
)

// unconnectedMessageSequence is a sequence of bytes which is found in every unconnected message sent in
// RakNet.
var unconnectedMessageSequence = [16]byte{0x00, 0xff, 0xff, 0x00, 0xfe, 0xfe, 0xfe, 0xfe, 0xfd, 0xfd, 0xfd, 0xfd, 0x12, 0x34, 0x56, 0x78}
