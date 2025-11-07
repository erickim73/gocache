package protocol

func EncodeString(s string) string {
	return "+" + s + "\r\n"
}