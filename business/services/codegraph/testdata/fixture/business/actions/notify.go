package actions

import "example.com/fixture/config"

// Notify returns the SMTP host messages go through. It reads a whole settings
// group and one key env.yml does not declare.
func Notify() string {
	host, _ := config.Get("mail.smtp")
	beta, _ := config.Get("feature.beta")
	return host + beta
}
