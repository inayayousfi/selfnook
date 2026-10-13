package app

import "strings"

// CurlInput is curl's configuration for "curl --config -": the request
// headers and, when not empty, the request body. Sent on curl's input, they
// stay out of its arguments, where any account on the computer can read
// them.
func CurlInput(headers []string, body string) string {
	var input strings.Builder
	for _, header := range headers {
		input.WriteString("header = " + curlQuote(header) + "\n")
	}
	if body != "" {
		input.WriteString("data-binary = " + curlQuote(body) + "\n")
	}
	return input.String()
}

func curlQuote(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(value) + `"`
}
