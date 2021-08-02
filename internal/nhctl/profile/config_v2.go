/*
* Copyright (C) 2021 THL A29 Limited, a Tencent company.  All rights reserved.
* This source code is licensed under the Apache License Version 2.0.
*/

package profile

import (
	"nocalhost/internal/nhctl/common/base"
	"nocalhost/internal/nhctl/fp"
	"nocalhost/pkg/nhctl/clientgoutils"
)

//type AppType string
//type SvcType string

type NocalHostAppConfigV2 struct {
	ConfigProperties  *ConfigProperties  `json:"configProperties" yaml:"configProperties"`
	ApplicationConfig *ApplicationConfig `json:"application" yaml:"application"`
}

type ConfigProperties struct {
	Version string `json:"version" yaml:"version"`
	EnvFile string `json:"envFile" yaml:"envFile"`
}

type ApplicationConfig struct {
	Name         string  `json:"name" yaml:"name,omitempty"`
	Type         string  `json:"manifestType" yaml:"manifestType,omitempty"`
	ResourcePath RelPath `json:"resourcePath" yaml:"resourcePath"`
	IgnoredPath  RelPath `json:"ignoredPath" yaml:"ignoredPath"`

	PreInstall  SortedRelPath `json:"onPreInstall" yaml:"onPreInstall"`
	PostInstall SortedRelPath `json:"onPostInstall" yaml:"onPostInstall"`
	PreUpgrade  SortedRelPath `json:"onPreUpgrade" yaml:"onPreUpgrade"`
	PostUpgrade SortedRelPath `json:"onPostUpgrade" yaml:"onPostUpgrade"`
	PreDelete   SortedRelPath `json:"onPreDelete" yaml:"onPreDelete"`
	PostDelete  SortedRelPath `json:"onPostDelete" yaml:"onPostDelete"`

	HelmValues     []*HelmValue       `json:"helmValues" yaml:"helmValues"`
	HelmVals       interface{}        `json:"helmVals" yaml:"helmVals"`
	HelmVersion    string             `json:"helmVersion" yaml:"helmVersion"`
	Env            []*Env             `json:"env" yaml:"env"`
	EnvFrom        EnvFrom            `json:"envFrom" yaml:"envFrom"`
	ServiceConfigs []*ServiceConfigV2 `json:"services" yaml:"services,omitempty"`
}

type ServiceConfigV2 struct {
	Name                string               `json:"name" yaml:"name"`
	Type                string               `json:"serviceType" yaml:"serviceType"`
	PriorityClass       string               `json:"priorityClass,omitempty" yaml:"priorityClass,omitempty"`
	DependLabelSelector *DependLabelSelector `json:"dependLabelSelector,omitempty" yaml:"dependLabelSelector,omitempty"`
	ContainerConfigs    []*ContainerConfig   `json:"containers" yaml:"containers"`
}

type ContainerConfig struct {
	Name    string                  `json:"name" yaml:"name"`
	Install *ContainerInstallConfig `json:"install,omitempty" yaml:"install,omitempty"`
	Dev     *ContainerDevConfig     `json:"dev" yaml:"dev"`
}

type ContainerInstallConfig struct {
	Env         []*Env   `json:"env" yaml:"env"`
	EnvFrom     EnvFrom  `json:"envFrom" yaml:"envFrom"`
	PortForward []string `json:"portForward" yaml:"portForward"`
}

type ContainerDevConfig struct {
	GitUrl                string                 `json:"gitUrl" yaml:"gitUrl"`
	Image                 string                 `json:"image" yaml:"image"`
	Shell                 string                 `json:"shell" yaml:"shell"`
	WorkDir               string                 `json:"workDir" yaml:"workDir"`
	StorageClass          string                 `json:"storageClass" yaml:"storageClass"`
	DevContainerResources *ResourceQuota         `json:"resources" yaml:"resources"`
	PersistentVolumeDirs  []*PersistentVolumeDir `json:"persistentVolumeDirs" yaml:"persistentVolumeDirs"`
	Command               *DevCommands           `json:"command" yaml:"command"`
	DebugConfig           *DebugConfig           `json:"debug" yaml:"debug"`
	UseDevContainer       bool                   `json:"useDevContainer" yaml:"useDevContainer"`
	Sync                  *SyncConfig            `json:"sync" yaml:"sync"`
	Env                   []*Env                 `json:"env" yaml:"env"`
	EnvFrom               *EnvFrom               `json:"envFrom" yaml:"envFrom"`
	PortForward           []string               `json:"portForward" yaml:"portForward"`
}

type DevCommands struct {
	Build          []string `json:"build" yaml:"build"`
	Run            []string `json:"run" yaml:"run"`
	Debug          []string `json:"debug" yaml:"debug"`
	HotReloadRun   []string `json:"hotReloadRun" yaml:"hotReloadRun"`
	HotReloadDebug []string `json:"hotReloadDebug" yaml:"hotReloadDebug"`
}

type SyncConfig struct {
	Type              string   `json:"type" yaml:"type"`
	FilePattern       []string `json:"filePattern" yaml:"filePattern"`
	IgnoreFilePattern []string `json:"ignoreFilePattern" yaml:"ignoreFilePattern"`
}

type DebugConfig struct {
	RemoteDebugPort int `json:"remoteDebugPort" yaml:"remoteDebugPort"`
}

type DependLabelSelector struct {
	Pods []string `json:"pods" yaml:"pods"`
	Jobs []string `json:"jobs" yaml:"jobs"`
}

type HelmValue struct {
	Key   string `json:"key" yaml:"key"`
	Value string `json:"value" yaml:"value"`
}

type Env struct {
	Name  string `json:"name" yaml:"name"`
	Value string `json:"value" yaml:"value"`
}

type EnvFrom struct {
	EnvFile []*EnvFile `json:"envFile" yaml:"envFile"`
}

type EnvFile struct {
	Path string `json:"path" yaml:"path"`
}

func (n *NocalHostAppConfigV2) GetSvcConfigV2(svcName string, svcType base.SvcType) *ServiceConfigV2 {
	for _, config := range n.ApplicationConfig.ServiceConfigs {
		if config.Name == svcName && base.SvcTypeOf(config.Type) == svcType {
			return config
		}
	}
	return nil
}

func (c *ApplicationConfig) LoadManifests(tmpDir *fp.FilePathEnhance) []string {
	if c == nil {
		return []string{}
	}

	return clientgoutils.LoadValidManifest(
		c.ResourcePath.Load(tmpDir),
		c.PreInstall.Load(tmpDir),
		c.PostInstall.Load(tmpDir),
		c.PreUpgrade.Load(tmpDir),
		c.PostUpgrade.Load(tmpDir),
		c.IgnoredPath.Load(tmpDir),
		c.PreDelete.Load(tmpDir),
		c.PostDelete.Load(tmpDir),
	)
}
