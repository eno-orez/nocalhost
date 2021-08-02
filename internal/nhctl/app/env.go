/*
* Copyright (C) 2020 THL A29 Limited, a Tencent company.  All rights reserved.
* This source code is licensed under the Apache License Version 2.0.
*/

package app

import (
	"nocalhost/internal/nhctl/profile"
	"nocalhost/pkg/nhctl/utils"
)

// Used by dep
type InstallEnvForDep struct {
	Global  []*profile.Env      `json:"global" yaml:"global"`
	Service []*ServiceEnvForDep `json:"service" yaml:"service"`
}

type ServiceEnvForDep struct {
	Name      string                `json:"name" yaml:"name"`
	Type      string                `json:"type" yaml:"type"`
	Container []*ContainerEnvForDep `json:"container" yaml:"container"`
}

type ContainerEnvForDep struct {
	Name       string         `json:"name" yaml:"name"`
	InstallEnv []*profile.Env `json:"installEnv" yaml:"installEnv"`
}

func (a *Application) GetInstallEnvForDep() *InstallEnvForDep {
	appProfileV2, _ := a.GetProfile()

	envFiles := make([]string, 0)
	for _, f := range appProfileV2.EnvFrom.EnvFile {
		envFiles = append(envFiles, f.Path)
	}
	kvMap := utils.GetKVFromEnvFiles(envFiles)

	// Env has a higher priority than envFrom
	for _, env := range appProfileV2.Env {
		kvMap[env.Name] = env.Value
	}

	globalEnv := make([]*profile.Env, 0)
	for key, val := range kvMap {
		globalEnv = append(globalEnv, &profile.Env{
			Name:  key,
			Value: val,
		})
	}

	// Find service env
	servcesEnv := make([]*ServiceEnvForDep, 0)
	for _, svcProfile := range appProfileV2.SvcProfile {
		if svcProfile.ServiceConfigV2 == nil || len(svcProfile.ServiceConfigV2.ContainerConfigs) == 0 {
			continue
		}
		svcEnv := &ServiceEnvForDep{
			Name:      svcProfile.ActualName,
			Type:      string(svcProfile.Type),
			Container: make([]*ContainerEnvForDep, 0),
		}
		for _, config := range svcProfile.ServiceConfigV2.ContainerConfigs {
			if config.Install == nil {
				continue
			}
			if len(config.Install.Env) == 0 && len(config.Install.EnvFrom.EnvFile) == 0 {
				continue
			}

			envFiles1 := make([]string, 0)
			for _, f := range config.Install.EnvFrom.EnvFile {
				envFiles1 = append(envFiles1, f.Path)
			}
			kvMap1 := utils.GetKVFromEnvFiles(envFiles1)

			// Env has a higher priority than envFrom
			for _, env := range config.Install.Env {
				kvMap1[env.Name] = env.Value
			}

			containerEnv := make([]*profile.Env, 0)
			for key, val := range kvMap1 {
				containerEnv = append(containerEnv, &profile.Env{
					Name:  key,
					Value: val,
				})
			}

			svcEnv.Container = append(svcEnv.Container, &ContainerEnvForDep{
				Name:       config.Name,
				InstallEnv: containerEnv,
			})
		}
		if len(svcEnv.Container) > 0 {
			servcesEnv = append(servcesEnv, svcEnv)
		}
	}

	installEnv := &InstallEnvForDep{
		Global:  globalEnv,
		Service: servcesEnv,
	}
	return installEnv
}
