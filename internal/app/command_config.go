package app

import (
	"github.com/Aethersailor/m-ui/internal/config"
	"github.com/Aethersailor/m-ui/internal/domain"
	"github.com/Aethersailor/m-ui/internal/store"
)

func initialSettings(cfg config.Config) store.InitialSettings {
	externalController, _ := domain.ParseEndpoint(cfg.Mihomo.ExternalControllerAddress)
	controllerConnect, _ := domain.ParseEndpoint(cfg.Mihomo.ControllerConnectAddress)
	if externalController.Port == 0 {
		legacy, err := domain.ParseEndpoint(cfg.Mihomo.ControllerAddress)
		if err == nil {
			externalController, controllerConnect, _ = domain.SplitLegacyControllerEndpoint(legacy)
		}
	}
	if controllerConnect.Port == 0 {
		_, controllerConnect, _ = domain.SplitLegacyControllerEndpoint(externalController)
	}
	return store.InitialSettings{
		PanelTitle:                       cfg.Panel.Title,
		UILanguage:                       cfg.Panel.UILanguage,
		PublicHost:                       cfg.Panel.PublicHost,
		PanelListenAddress:               cfg.Server.ListenAddress,
		PanelListenPort:                  cfg.Server.Port,
		MihomoExternalControllerBindHost: externalController.Host,
		MihomoExternalControllerBindPort: externalController.Port,
		MihomoControllerConnectHost:      controllerConnect.Host,
		MihomoControllerConnectPort:      controllerConnect.Port,
		ExternalControllerCORSOrigins:    append([]string(nil), cfg.Mihomo.ExternalControllerCORSOrigins...),
		TrustedProxyCIDRs:                []string{},
		MihomoBinaryPath:                 cfg.Mihomo.BinaryPath,
		MihomoConfigDir:                  cfg.Mihomo.ConfigDirectory,
		MihomoConfigPath:                 cfg.Mihomo.ConfigPath,
		ControllerAddress:                cfg.Mihomo.ControllerAddress,
		BootstrapSecret:                  cfg.Mihomo.ControllerSecret,
		MihomoServiceName:                cfg.Mihomo.ServiceName,
		HistoryLimit:                     cfg.Mihomo.HistoryLimit,
	}
}
