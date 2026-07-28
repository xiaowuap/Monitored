package notifier

import "encoding/base64"

func base64Raw(value []byte) string {
	return base64.StdEncoding.EncodeToString(value)
}
