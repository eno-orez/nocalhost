/*
* Copyright (C) 2021 THL A29 Limited, a Tencent company.  All rights reserved.
* This source code is licensed under the Apache License Version 2.0.
 */

package dev

import (
	"context"
	"fmt"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"nocalhost/cmd/nhctl/cmds/common"
	"nocalhost/internal/nhctl/coloredoutput"
	"nocalhost/internal/nhctl/common/base"
	_const "nocalhost/internal/nhctl/const"
	"nocalhost/internal/nhctl/controller"
	"nocalhost/internal/nhctl/dev_dir"
	"nocalhost/internal/nhctl/model"
	"nocalhost/internal/nhctl/nocalhost"
	"nocalhost/internal/nhctl/nocalhost_path"
	"nocalhost/internal/nhctl/profile"
	"nocalhost/internal/nhctl/syncthing"
	"nocalhost/internal/nhctl/utils"
	"nocalhost/pkg/nhctl/log"
	utils2 "nocalhost/pkg/nhctl/utils"
	"strconv"
	"strings"
)

var (
	pod   string
	shell string
)

type DevStartOps struct {
	*model.DevStartOptions
	nocalhostSvc *controller.Controller
}

var devStartOps = &model.DevStartOptions{}

func init() {

	DevStartCmd.Flags().StringVarP(
		&common.WorkloadName, "deployment", "d", "",
		"k8s deployment your developing service exists",
	)
	DevStartCmd.Flags().StringVarP(
		&common.ServiceType, "controller-type", "t", "deployment",
		"kind of k8s controller,such as deployment,statefulSet",
	)
	DevStartCmd.Flags().StringVarP(
		&devStartOps.DevImage, "image", "i", "",
		"image of DevContainer",
	)
	DevStartCmd.Flags().StringVarP(
		&devStartOps.Container, "container", "c", "",
		"container to develop",
	)
	//DevStartCmd.Flags().StringVar(&devStartOps.WorkDir, "work-dir", "", "container's work directory")
	DevStartCmd.Flags().StringVar(&devStartOps.StorageClass, "storage-class", "", "StorageClass used by PV")
	DevStartCmd.Flags().StringVar(
		&devStartOps.PriorityClass, "priority-class", "", "PriorityClass used by devContainer",
	)
	// for debug only
	DevStartCmd.Flags().StringVar(
		&devStartOps.SyncthingVersion, "syncthing-version", "",
		"versions of syncthing and this flag is use for debug only",
	)
	// local absolute paths to sync
	DevStartCmd.Flags().StringSliceVarP(
		&devStartOps.LocalSyncDir, "local-sync", "s", []string{},
		"local directory to sync",
	)
	DevStartCmd.Flags().StringVarP(
		&shell, "shell", "", "",
		"use current shell cmd to enter terminal while dev start success",
	)
	DevStartCmd.Flags().BoolVar(
		&devStartOps.NoTerminal, "without-terminal", false,
		"do not enter terminal directly while dev start success",
	)
	DevStartCmd.Flags().BoolVar(
		&devStartOps.NoSyncthing, "without-sync", false,
		"do not start file-sync while dev start success",
	)
	DevStartCmd.Flags().StringVarP(
		&devStartOps.DevModeType, "dev-mode", "m", "",
		"specify which DevMode you want to enter, such as: replace,duplicate. Default: replace",
	)
}

var DevStartCmd = &cobra.Command{
	Use:   "start [NAME]",
	Short: "Start DevMode",
	Long:  `Start DevMode`,
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) < 1 {
			return errors.Errorf("%q requires at least 1 argument\n", cmd.CommandPath())
		}
		return nil
	},
	Run: func(cmd *cobra.Command, args []string) {
		d := DevStartOps{DevStartOptions: devStartOps}
		must(d.StartDevMode(args[0]))
	},
}

func (d *DevStartOps) StartDevMode(applicationName string) error {

	dt := profile.DevModeType(d.DevModeType)
	if !dt.IsDuplicateDevMode() && !dt.IsReplaceDevMode() {
		return errors.New(fmt.Sprintf("Unsupported DevModeType %s", dt))
	}

	if len(d.LocalSyncDir) > 1 {
		log.Fatal("Can not define multi 'local-sync(-s)'")
	} else if len(d.LocalSyncDir) == 0 {
		log.Fatal("'local-sync(-s)' must be specified")
	}

	nocalhostSvc, err := common.InitAppAndCheckIfSvcExist(applicationName, common.WorkloadName, common.ServiceType)
	if err != nil {
		return err
	}
	d.nocalhostSvc = nocalhostSvc

	if !common.NocalhostApp.GetAppMeta().IsInstalled() {
		log.Fatal(common.NocalhostApp.GetAppMeta().NotInstallTips())
	}

	if d.nocalhostSvc.IsInDevMode() {
		coloredoutput.Hint(fmt.Sprintf("Already in %s DevMode...", d.nocalhostSvc.DevModeType.ToString()))

		podName, err := d.nocalhostSvc.GetDevModePodName()
		must(err)

		if !d.NoSyncthing {
			if d.nocalhostSvc.IsProcessor() {
				d.startSyncthing(podName, true)
			}
		} else {
			coloredoutput.Success("File sync is not resumed caused by --without-sync flag.")
		}

		if d.nocalhostSvc.IsProcessor() {
			if !d.NoTerminal || shell != "" {
				must(d.nocalhostSvc.EnterPodTerminal(podName, d.Container, shell, ""))
			}
			return nil
		}
	}

	// 1) reload svc config from local if needed
	// 2) stop previous syncthing
	// 3) recording profile
	// 4) mark app meta as developing
	// 5) initial syncthing runtime env
	// 6) stop port-forward
	// 7) enter developing (replace image)
	// 8) port forward for dev-container
	// 9) start syncthing
	// 10) entering dev container

	coloredoutput.Hint(fmt.Sprintf("Starting %s DevMode...", dt.ToString()))

	d.nocalhostSvc.DevModeType = dt
	d.loadLocalOrCmConfigIfValid()
	d.stopPreviousSyncthing()
	d.recordLocalSyncDirToProfile()
	d.prepareSyncThing()
	d.stopPreviousPortForward()
	if err := d.enterDevMode(dt); err != nil {
		log.FatalE(err, "")
	}

	devPodName, err := d.nocalhostSvc.GetDevModePodName()
	must(err)

	d.startPortForwardAfterDevStart(devPodName)

	if !d.NoSyncthing {
		d.startSyncthing(devPodName, false)
	} else {
		coloredoutput.Success("File sync is not started caused by --without-sync flag..")
	}

	if !d.NoTerminal || shell != "" {
		must(d.nocalhostSvc.EnterPodTerminal(devPodName, "", shell, ""))
	}
	return nil
}

var pfListBeforeDevStart []*profile.DevPortForward

func (d *DevStartOps) stopPreviousPortForward() {
	appProfile, _ := common.NocalhostApp.GetProfile()
	pfListBeforeDevStart = appProfile.SvcProfileV2(common.WorkloadName, string(d.nocalhostSvc.Type)).DevPortForwardList
	for _, pf := range pfListBeforeDevStart {
		log.Infof("Stopping %d:%d", pf.LocalPort, pf.RemotePort)
		utils.Should(d.nocalhostSvc.EndDevPortForward(pf.LocalPort, pf.RemotePort))
	}
}

func (d *DevStartOps) prepareSyncThing() {
	dt := profile.DevModeType(d.DevModeType)
	must(d.nocalhostSvc.CreateSyncThingSecret(d.Container, d.LocalSyncDir, dt.IsDuplicateDevMode()))
}

func (d *DevStartOps) recordLocalSyncDirToProfile() {
	must(
		d.nocalhostSvc.UpdateSvcProfile(
			func(svcProfile *profile.SvcProfileV2) error {
				svcProfile.LocalAbsoluteSyncDirFromDevStartPlugin = d.LocalSyncDir
				return nil
			},
		),
	)
}

// when re enter dev mode, nocalhost will check the associate dir
// nocalhost will load svc config from associate dir if needed
func (d *DevStartOps) loadLocalOrCmConfigIfValid() {

	svcPack := dev_dir.NewSvcPack(
		d.nocalhostSvc.NameSpace,
		d.nocalhostSvc.AppMeta.NamespaceId,
		d.nocalhostSvc.AppName,
		d.nocalhostSvc.Type,
		d.nocalhostSvc.Name,
		d.Container,
	)

	switch len(d.LocalSyncDir) {
	case 0:
		associatePath := svcPack.GetAssociatePath()
		if associatePath == "" {
			must(errors.New("'local-sync(-s)' should specify while svc is not associate with local dir"))
		}
		d.LocalSyncDir = append(d.LocalSyncDir, string(associatePath))

		must(associatePath.Associate(svcPack, common.KubeConfig, true))

		_ = common.NocalhostApp.ReloadSvcCfg(common.WorkloadName, base.SvcType(common.ServiceType), false, false)
	case 1:

		must(dev_dir.DevPath(d.LocalSyncDir[0]).Associate(svcPack, common.KubeConfig, true))

		_ = common.NocalhostApp.ReloadSvcCfg(common.WorkloadName, base.SvcType(common.ServiceType), false, false)
	default:
		log.Fatal(errors.New("Can not define multi 'local-sync(-s)'"))
	}
}

// we should clean previous Syncthing
// prevent previous syncthing hold the db lock
func (d *DevStartOps) stopPreviousSyncthing() {
	// Clean up previous syncthing
	must(
		d.nocalhostSvc.FindOutSyncthingProcess(
			func(pid int) error {
				return syncthing.Stop(pid, true)
			},
		),
	)
	// kill syncthing process by find find it with terminal
	str := strings.ReplaceAll(d.nocalhostSvc.GetSyncDir(), nocalhost_path.GetNhctlHomeDir(), "")
	utils2.KillSyncthingProcess(str)
}

func (d *DevStartOps) startSyncthing(podName string, resume bool) {
	d.StartSyncthing(podName, resume, false, nil, false)
	if resume {
		coloredoutput.Success("File sync resumed")
	} else {
		coloredoutput.Success("File sync started")
	}
}

func (d *DevStartOps) enterDevMode(devModeType profile.DevModeType) error {
	must(
		d.nocalhostSvc.AppMeta.SvcDevStarting(
			d.nocalhostSvc.Name, d.nocalhostSvc.Type,
			common.NocalhostApp.Identifier, devModeType,
		),
	)
	must(
		d.nocalhostSvc.UpdateSvcProfile(
			func(v2 *profile.SvcProfileV2) error {
				v2.OriginDevContainer = d.Container
				return nil
			},
		),
	)

	// prevent dev status modified but not actually enter dev mode
	var devStartSuccess = false
	var err error
	defer func() {
		if !devStartSuccess {
			log.Infof("Roll backing dev mode...")
			_ = d.nocalhostSvc.AppMeta.SvcDevEnd(
				d.nocalhostSvc.Name, d.nocalhostSvc.Identifier, d.nocalhostSvc.Type, devModeType,
			)
		}
	}()

	// Only `replace` DevMode needs to disable hpa
	if devModeType.IsReplaceDevMode() {
		log.Info("Disabling hpa...")
		hl, err := d.nocalhostSvc.ListHPA()
		if err != nil {
			log.WarnE(err, "Failed to find hpa")
		}
		if len(hl) == 0 {
			log.Info("No hpa found")
		}
		for _, h := range hl {
			if len(h.Annotations) == 0 {
				h.Annotations = make(map[string]string)
			}
			h.Annotations[_const.HPAOriginalMaxReplicasKey] = strconv.Itoa(int(h.Spec.MaxReplicas))
			h.Spec.MaxReplicas = 1
			if h.Spec.MinReplicas != nil {
				h.Annotations[_const.HPAOriginalMinReplicasKey] = strconv.Itoa(int(*h.Spec.MinReplicas))
				var i int32 = 1
				h.Spec.MinReplicas = &i
			}
			if _, err = d.nocalhostSvc.Client.UpdateHPA(&h); err != nil {
				log.WarnE(err, fmt.Sprintf("Failed to update hpa %s", h.Name))
			} else {
				log.Infof("HPA %s has been disabled", h.Name)
			}
		}
	}

	//d.nocalhostSvc.DevModeType = devModeType
	if err = d.nocalhostSvc.BuildPodController().ReplaceImage(context.TODO(), d.DevStartOptions); err != nil {
		log.WarnE(err, "Failed to replace dev container")
		log.Info("Resetting workload...")
		_ = d.nocalhostSvc.DevEnd(true)
		if errors.Is(err, nocalhost.CreatePvcFailed) {
			log.Info("Failed to provision persistent volume due to insufficient resources")
		}
		return err
	}

	if err = d.nocalhostSvc.AppMeta.SvcDevStartComplete(
		d.nocalhostSvc.Name, d.nocalhostSvc.Type, d.nocalhostSvc.Identifier, devModeType,
	); err != nil {
		return err
	}

	utils.Should(d.nocalhostSvc.IncreaseDevModeCount())

	// mark dev start as true
	devStartSuccess = true
	coloredoutput.Success("Dev container has been updated")
	return nil
}

func (d *DevStartOps) startPortForwardAfterDevStart(devPodName string) {
	for _, pf := range pfListBeforeDevStart {
		utils.Should(d.nocalhostSvc.PortForward(devPodName, pf.LocalPort, pf.RemotePort, pf.Role))
	}
	must(d.nocalhostSvc.PortForwardAfterDevStart(devPodName, d.Container))
}

func must(err error) {
	mustI(err, "")
}

func mustI(err error, info string) {
	if k8serrors.IsForbidden(err) {
		log.FatalE(err, "Permission Denied! Please check that"+
			" your ServiceAccount(KubeConfig) has appropriate permissions.\n\n")
	} else if err != nil {
		log.FatalE(err, info)
	}
}
