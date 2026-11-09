// Packet layout, string padding and boolean tags: OSC 1.0,
// https://opensoundcontrol.stanford.edu/spec-1_0.html
// /chatbox/input arguments: VRChat's OSC interface,
// https://docs.vrchat.com/docs/osc-as-input-controller#chatbox
// T sends at once. F turns off the notification sound.
// OSC 1.0 specifies ASCII strings. VRChat reads the text as UTF-8.

package chatbox

func encodePage(text string) []byte {
	packet := appendOSCString(nil, "/chatbox/input")
	packet = appendOSCString(packet, ",sTF")
	return appendOSCString(packet, text)
}

func appendOSCString(packet []byte, text string) []byte {
	packet = append(packet, text...)
	return append(packet, make([]byte, 4-len(text)%4)...)
}
