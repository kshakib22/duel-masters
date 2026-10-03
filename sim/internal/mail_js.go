//go:build js

package internal

import "errors"

// SendMail is not available in the browser build, which leaves out the AWS
// library mail.go needs.
func SendMail(to string, subject string, body string) error {
	return errors.New("sending email is not available in the browser build")
}
