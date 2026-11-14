package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/b1ug/nb1/internal/appconfig"
	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func profileSelection(cmd *cobra.Command) (appconfig.Selection, error) {
	flag := ""
	if cmd.Flags().Lookup("config") != nil {
		flag, _ = cmd.Flags().GetString("config")
		if cmd.Flags().Changed("config") && flag == "" {
			return appconfig.Selection{}, fmt.Errorf("--config must name a file")
		}
	}
	return appconfig.Select(flag, os.Getenv("NB1_CONFIG"))
}

func applicationConfigSelection(cmd *cobra.Command) (appconfig.Selection, error) {
	selected, err := profileSelection(cmd)
	if err != nil {
		return selected, err
	}
	// Preserve the established setting.yaml lookup only when no new profile
	// exists. The legacy file is still validated before new runtime operations.
	if _, err := os.Lstat(selected.Path); os.IsNotExist(err) && !selected.Required && viper.ConfigFileUsed() != "" {
		if path, err := filepath.Abs(viper.ConfigFileUsed()); err == nil {
			if _, err := os.Lstat(path); os.IsNotExist(err) {
				return selected, nil
			}
			selected.Path, selected.Required = path, true
		}
	}
	return selected, nil
}

func applicationSettings(cmd *cobra.Command) (appconfig.Config, appconfig.Selection, error) {
	selected, err := applicationConfigSelection(cmd)
	if err != nil {
		return appconfig.Config{}, selected, err
	}
	settings, err := appconfig.Load(selected)
	return settings, selected, err
}

func preferredSerial(cmd *cobra.Command, settings appconfig.Config) (string, error) {
	selected := settings.Device
	if value, exists := os.LookupEnv("NB1_DEVICE"); exists && value != "" {
		selected = value
	}
	if cmd.Flags().Lookup("device") != nil && cmd.Flags().Changed("device") {
		selected, _ = cmd.Flags().GetString("device")
		if selected == "" {
			return "", fmt.Errorf("--device must name a serial")
		}
	}
	if selected != "" && !control.ValidDeviceSerial(selected) {
		return "", fmt.Errorf("device must be a valid serial")
	}
	if selected != "" {
		settings.Device = selected
		if err := appconfig.Validate(settings); err != nil {
			return "", err
		}
	}
	return selected, nil
}

func configuredServerSerials(cmd *cobra.Command, settings appconfig.Config, flags []string) ([]string, error) {
	if cmd.Flags().Changed("devices") {
		if len(flags) == 0 {
			return nil, fmt.Errorf("--devices must select at least one device")
		}
		settings.Server.Devices = flags
		if err := appconfig.Validate(settings); err != nil {
			return nil, err
		}
		return append([]string(nil), flags...), nil
	}
	preferred, err := preferredSerial(cmd, settings)
	if err != nil {
		return nil, err
	}
	explicitDevice := os.Getenv("NB1_DEVICE") != "" || (cmd.Flags().Lookup("device") != nil && cmd.Flags().Changed("device"))
	if !explicitDevice && len(settings.Server.Devices) > 0 {
		return append([]string(nil), settings.Server.Devices...), nil
	}
	if preferred != "" {
		return []string{preferred}, nil
	}
	return nil, fmt.Errorf("select devices with --devices, --device or the user configuration; the daemon does not choose the first HID device")
}

func resolveControlSerial(ctx context.Context, client *daemonclient.Client, cmd *cobra.Command, settings appconfig.Config, explicit string) (string, error) {
	if explicit != "" {
		if !control.ValidDeviceSerial(explicit) {
			return "", fmt.Errorf("invalid device serial")
		}
		return explicit, nil
	}
	preferred, err := preferredSerial(cmd, settings)
	if err != nil || preferred != "" {
		return preferred, err
	}
	devices, err := client.Devices(ctx)
	if err != nil {
		return "", err
	}
	if len(devices) == 1 {
		return devices[0].Serial, nil
	}
	serials := make([]string, 0, len(devices))
	for _, device := range devices {
		serials = append(serials, device.Serial)
	}
	return "", fmt.Errorf("select a device serial with an argument, --device or configuration; daemon has %d configured devices: %s", len(devices), strings.Join(serials, ", "))
}
