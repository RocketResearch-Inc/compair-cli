package config

import "os"

func ResolveAPIBase(cliProfile, cliAPIBase string) (string, string, string, error) {
	if cliAPIBase != "" {
		return cliAPIBase, "(--api-base)", cliProfile, nil
	}
	if env := os.Getenv("COMPAIR_API_BASE"); env != "" {
		return env, "(COMPAIR_API_BASE)", cliProfile, nil
	}

	profName := cliProfile
	if profName == "" {
		if envProfile := os.Getenv("COMPAIR_PROFILE"); envProfile != "" {
			profName = envProfile
		}
	}
	profs, err := LoadProfiles()
	if err != nil {
		return "", "", "", err
	}
	if profName == "" {
		profName = profs.Default
	}
	prof, ok := profs.Profiles[profName]
	if !ok {
		return "", "", profName, nil
	}
	return prof.APIBase, "(profile:" + profName + ")", profName, nil
}
