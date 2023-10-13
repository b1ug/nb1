package config

import "github.com/spf13/viper"

// This file contains the mutable configuration for the application.

/*
Steps to add new config property
1. (Optional) Add a const for default value, if it's also used in flags.
2. Add viper.SetDefault() in SetDefaults() with default value with implicit type
3. Add getter func with return type, like GetTitle() string
4. (Optional) Add viper.BindPFlag() with Flags().Lookup() and flag, if you want to bind it to a flag
5. Use it via the getter func in the elsewhere of the application
*/

const (
	// DefaultConfigFile is the default configuration file name.
	DefaultConfigFile = "setting.yaml"
	// DefaultPort is the default port number of the web server. HACK: uint32 is used to avoid viper's bug for uint16.
	DefaultPort = uint32(8080)
)

// SetDefaults sets the default values in Viper for the configuration.
func SetDefaults() {
	viper.SetDefault("title", "Standard Application")
	viper.SetDefault("base_url", "")
	viper.SetDefault("port", DefaultPort)
	viper.SetDefault("content_dir", "")
}

// GetTitle returns the title of the application for website.
func GetTitle() string {
	return viper.GetString("title")
}

// GetBaseURL returns the base URL of the web server.
func GetBaseURL() string {
	return viper.GetString("base_url")
}

// GetPort returns the port number of the web server.
func GetPort() uint32 {
	return viper.GetUint32("port")
}

// GetContentDir returns the filesystem path to the content directory.
func GetContentDir() string {
	return viper.GetString("content_dir")
}
