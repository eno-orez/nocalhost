/*
* Copyright (C) 2021 THL A29 Limited, a Tencent company.  All rights reserved.
* This source code is licensed under the Apache License Version 2.0.
 */

package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gdamore/tcell/v2"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"nocalhost/internal/nhctl/app_flags"
	"nocalhost/internal/nhctl/appmeta"
	"nocalhost/internal/nhctl/common"
	"nocalhost/internal/nhctl/daemon_client"
	"nocalhost/internal/nhctl/daemon_handler/item"
	"nocalhost/internal/nhctl/nocalhost"
	"nocalhost/internal/nhctl/utils"
	"nocalhost/pkg/nhctl/log"
	"os"
	"reflect"
	"strconv"
	"strings"
	//"github.com/rivo/tview"
	"github.com/derailed/tview"
)

const (
	clusterInfoWidth = 50
	clusterInfoPad   = 15

	// Main Menu
	deployApplicationOption = " Deploy Application"
	switchContextOption     = " Switch Context"
	selectAppOption         = " List applications"

	deployDemoAppOption      = " Quickstart: Deploy BookInfo demo application"
	deployHelmAppOption      = " Helm: Use my own Helm chart (e.g. local via ./chart/ or any remote chart)"
	deployKubectlAppOption   = " Kubectl: Use existing Kubernetes manifests (e.g. ./kube/deployment.yaml)"
	deployKustomizeAppOption = " Kustomize: Use an existing Kustomization (e.g. ./kube/kustomization/)"
)

func RunTviewApplication() {
	app := NewTviewApplication()
	if err := app.Run(); err != nil {
		return
	}
}

func (t *TviewApplication) buildMainMenu() tview.Primitive {
	mainMenu := NewBorderedTable(" Menu")
	mainMenu.SetCell(0, 0, coloredCell(deployApplicationOption))
	mainMenu.SetCell(1, 0, coloredCell(selectAppOption))
	mainMenu.SetCell(2, 0, coloredCell(" Start DevMode Here"))
	mainMenu.SetCell(3, 0, coloredCell(switchContextOption))

	// Make selected eventHandler the same as clicked
	mainMenu.SetSelectedFunc(func(row, column int) {
		selectedCell := mainMenu.GetCell(row, column)
		var m tview.Primitive
		switch selectedCell.Text {
		case deployApplicationOption:
			m = t.buildDeployApplicationMenu()
		case switchContextOption:
			m = t.buildSelectContextMenu()
		case selectAppOption:
			m = t.buildSelectAppList()
		default:
			return
		}
		t.switchBodyToC(mainMenu, m)
	})
	return mainMenu
}

func (t *TviewApplication) buildSelectAppList() *tview.Table {
	selectAppTable := NewBorderedTable(" Select a application")

	metas, err := nocalhost.GetApplicationMetas(t.clusterInfo.NameSpace, t.clusterInfo.KubeConfig)
	if err != nil {
		panic(err)
	}

	for col, section := range []string{" Application", "State", "Type"} {
		selectAppTable.SetCell(0, col, infoCell(section))
	}

	for i, c := range metas {
		selectAppTable.SetCell(i+1, 0, coloredCell(" "+c.Application))
		selectAppTable.SetCell(i+1, 1, coloredCell(string(c.ApplicationState)))
		selectAppTable.SetCell(i+1, 2, coloredCell(string(c.ApplicationType)))
	}

	selectAppTable.SetSelectedFunc(func(row, column int) {
		if row > 0 {
			nextTable := t.buildSelectWorkloadList(metas[row-1])
			t.switchBodyToC(selectAppTable, nextTable)
		}
	})
	selectAppTable.Select(1, 0)
	return selectAppTable
}

func (t *TviewApplication) buildSelectWorkloadList(appMeta *appmeta.ApplicationMeta) *tview.Table {
	workloadListTable := NewBorderedTable(" Select a workload")

	cli, err := daemon_client.GetDaemonClient(utils.IsSudoUser())
	if err != nil {
		showErr(err)
		return nil
	}

	data, err := cli.SendGetResourceInfoCommand(
		t.clusterInfo.KubeConfig, t.clusterInfo.NameSpace, appMeta.Application, "deployment",
		"", nil, false)

	bytes, err := json.Marshal(data)
	if err != nil {
		showErr(err)
		return nil
	}

	multiple := reflect.ValueOf(data).Kind() == reflect.Slice
	var items []item.Item
	var item2 item.Item
	if multiple {
		_ = json.Unmarshal(bytes, &items)
	} else {
		_ = json.Unmarshal(bytes, &item2)
	}
	if !multiple {
		items = append(items, item2)
	}

	for col, section := range []string{" Name", "Kind", "DevMode", "DevStatus", "Syncing", "SyncGUIPort", "PortForward"} {
		workloadListTable.SetCell(0, col, infoCell(section))
	}

	for i, it := range items {
		workloadListTable.SetCell(i+1, 0, coloredCell(" "+"name"))
		um, ok := it.Metadata.(map[string]interface{})
		if !ok {
			continue
		}

		name, ok, err := unstructured.NestedString(um, "metadata", "name")
		if err != nil || !ok {
			continue
		}
		workloadListTable.SetCell(i+1, 0, coloredCell(" "+name))

		kind, _, _ := unstructured.NestedString(um, "kind")
		if kind != "" {
			workloadListTable.SetCell(i+1, 1, coloredCell(kind))
		}

		if it.Description == nil {
			continue
		}

		workloadListTable.SetCell(i+1, 2, coloredCell(string(it.Description.DevModeType)))
		workloadListTable.SetCell(i+1, 3, coloredCell(it.Description.DevelopStatus))
		workloadListTable.SetCell(i+1, 4, coloredCell(strconv.FormatBool(it.Description.Syncing)))
		workloadListTable.SetCell(i+1, 5, coloredCell(strconv.Itoa(it.Description.LocalSyncthingGUIPort)))
		pfList := make([]string, 0)
		for _, forward := range it.Description.DevPortForwardList {
			pfList = append(pfList, fmt.Sprintf("%d->%d", forward.LocalPort, forward.RemotePort))
		}
		workloadListTable.SetCell(i+1, 6, coloredCell(fmt.Sprintf("%v", pfList)))

		//workloadListTable.SetCell(i+1, 2, coloredCell(it.Description.DevelopStatus))
	}

	var selectAppFunc = func(row, column int) {
		if row > 0 {
			//selectedMeta := metas[row-1]
			//nsTable := NewBorderedTable("Select a workload")
			//cli, err := daemon_client.GetDaemonClient(utils.IsSudoUser())
			//if err != nil {
			//	showErr(err)
			//	return
			//}
			//data, err := cli.SendGetResourceInfoCommand(
			//	t.clusterInfo.KubeConfig, t.clusterInfo.NameSpace, selectedMeta.Application, "deployment",
			//	"", nil, false)
			//
			//nsTable.SetSelectedFunc(func(row, column int) {
			//	cell := nsTable.GetCell(row, column)
			//	t.clusterInfo.NameSpace = strings.Trim(cell.Text, " ")
			//	t.clusterInfo.k8sClient.NameSpace(strings.Trim(cell.Text, " "))
			//	t.RefreshHeader()
			//	t.switchMainMenu()
			//})
			//t.switchBodyTo(nsTable)
		}
	}
	workloadListTable.SetSelectedFunc(selectAppFunc)
	workloadListTable.Select(1, 0)
	return workloadListTable
}

func showErr(err error) {
	panic(err)
}

func (t *TviewApplication) buildSelectContextMenu() *tview.Table {
	table := NewBorderedTable(" Select a context")

	for col, section := range []string{" Context", "Cluster", "User", "NameSpace", "K8s Rev"} {
		table.SetCell(0, col, infoCell(section))
	}
	cs := loadAllClusterInfos()
	for i, c := range cs {
		table.SetCell(i+1, 0, coloredCell(" "+c.Context))
		table.SetCell(i+1, 1, coloredCell(c.Cluster))
		table.SetCell(i+1, 2, coloredCell(c.User))
		table.SetCell(i+1, 3, coloredCell(c.NameSpace))
		table.SetCell(i+1, 4, coloredCell(c.K8sVer))
	}

	table.SetSelectedFunc(func(row, column int) {
		if row > 0 {
			if len(cs) >= row {
				t.clusterInfo = cs[row-1]
				if !t.clusterInfo.k8sClient.IsClusterAdmin() {
					t.RefreshHeader()
					t.switchMainMenu()
					return
				}
				ns, err := t.clusterInfo.k8sClient.ClientSet.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
				if err != nil {
					return
				}
				nsTable := NewBorderedTable("Select a namespace")
				for i, item := range ns.Items {
					nsTable.SetCell(i, 0, coloredCell(" "+item.Name))
				}
				nsTable.SetSelectedFunc(func(row, column int) {
					cell := nsTable.GetCell(row, column)
					t.clusterInfo.NameSpace = strings.Trim(cell.Text, " ")
					t.clusterInfo.k8sClient.NameSpace(strings.Trim(cell.Text, " "))
					t.RefreshHeader()
					t.switchMainMenu()
				})
				t.switchBodyToC(table, nsTable)
			}
		}
	})

	table.Select(1, 0)
	return table
}

type SyncBuilder struct {
	WriteFunc func(p []byte) (int, error)
}

func (s *SyncBuilder) Write(p []byte) (int, error) {
	return s.WriteFunc(p)
}

func (s *SyncBuilder) Sync() error {
	return nil
}

func (t *TviewApplication) buildDeployApplicationMenu() *tview.Table {
	menu := NewBorderedTable(" Deploy Application")
	menu.SetCell(0, 0, coloredCell(deployDemoAppOption))
	menu.SetCell(1, 0, coloredCell(deployHelmAppOption))
	menu.SetCell(2, 0, coloredCell(deployKubectlAppOption))
	menu.SetCell(3, 0, coloredCell(deployKustomizeAppOption))

	menu.SetSelectedFunc(func(row, column int) {
		selectedCell := menu.GetCell(row, column)
		var m tview.Primitive
		switch selectedCell.Text {
		case deployDemoAppOption:
			view := tview.NewTextView()
			view.SetTitle(" Deploy BookInfo demo application")
			view.SetBorder(true)
			//view.SetText(fmt.Sprintf(" nhctl install bookinfo --git-url https://github.com/nocalhost/bookinfo.git --type rawManifest --kubeconfig %s --namespace %s\n", t.clusterInfo.KubeConfig, t.clusterInfo.NameSpace))
			view.SetScrollable(true)
			m = view
			f := app_flags.InstallFlags{
				GitUrl:  "https://github.com/nocalhost/bookinfo.git",
				AppType: string(appmeta.ManifestGit),
			}

			sbd := SyncBuilder{func(p []byte) (int, error) {
				t.app.QueueUpdateDraw(func() {
					view.Write([]byte(" " + string(p)))
				})
				return 0, nil
			}}

			log.RedirectionDefaultLogger(&sbd)
			go func() {
				_, err := common.InstallApplication(&f, "bookinfo", t.clusterInfo.KubeConfig, t.clusterInfo.NameSpace)
				if err != nil {
					panic(errors.Wrap(err, t.clusterInfo.KubeConfig))
				}
				log.RedirectionDefaultLogger(os.Stdout)
			}()
		default:
			return
		}
		t.switchBodyToC(menu, m)
	})
	return menu
}

func coloredCell(t string) *tview.TableCell {
	cell := tview.NewTableCell(t)
	cell.SetTextColor(tcell.ColorPaleGreen)
	return cell
}

func infoCell(t string) *tview.TableCell {
	cell := tview.NewTableCell(t)
	cell.SetExpansion(2)
	return cell
}

func sectionCell(t string) *tview.TableCell {
	cell := tview.NewTableCell(t + ":")
	cell.SetAlign(tview.AlignLeft)
	cell.SetTextColor(tcell.ColorPaleGreen)
	return cell
}

func keyCell(t string) *tview.TableCell {
	cell := tview.NewTableCell("<" + t + ">")
	cell.SetAlign(tview.AlignLeft)
	cell.SetTextColor(tcell.ColorPaleGreen)
	return cell
}
