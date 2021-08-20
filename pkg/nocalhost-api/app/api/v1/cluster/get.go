/*
* Copyright (C) 2021 THL A29 Limited, a Tencent company.  All rights reserved.
* This source code is licensed under the Apache License Version 2.0.
 */

package cluster

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/spf13/cast"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/wait"
	"nocalhost/internal/nocalhost-api/cache"
	"nocalhost/internal/nocalhost-api/model"
	"nocalhost/internal/nocalhost-api/service"
	"nocalhost/pkg/nocalhost-api/app/api"
	"nocalhost/pkg/nocalhost-api/app/api/v1/cluster_user"
	"nocalhost/pkg/nocalhost-api/app/router/ginbase"
	"nocalhost/pkg/nocalhost-api/app/router/middleware"
	"nocalhost/pkg/nocalhost-api/pkg/clientgo"
	"nocalhost/pkg/nocalhost-api/pkg/errno"
	"nocalhost/pkg/nocalhost-api/pkg/log"
	"strconv"
	"sync"
	"time"
)

type ClusterStatus struct {
	ClusterId       uint64
	Ready           bool
	NotReadyMessage string
}

type ClusterSafeList struct {
	ClusterList []*model.ClusterList
	Lock        *sync.Mutex
}

// GetList Get the cluster list
// @Summary Get the cluster list
// @Description Get the cluster list
// @Tags Cluster
// @Accept  json
// @Produce  json
// @param Authorization header string true "Authorization"
// @Success 200 {object} model.ClusterListVo "{"code":0,"message":"OK","data":model.ClusterListVo}"
// @Router /v1/cluster [get]
func GetList(c *gin.Context) {
	result, _ := service.Svc.ClusterSvc().GetList(c)
	tempResult := make([]*model.ClusterList, 0, 0)
	userId := c.GetUint64("userId")
	// normal user can only see clusters they created, or devSpace's cluster
	if isAdmin, _ := middleware.IsAdmin(c); !isAdmin {
		// cluster --> userid, find cluster which user's devSpace based on
		clusterToUser := make(map[uint64]uint64)
		// get clusters which associated with current user, like cluster which current user's devSpace based on
		lists, _ := cluster_user.DoList(&model.ClusterUserModel{}, userId, false)
		for _, i := range lists {
			clusterToUser[i.ClusterId] = i.ClusterId
		}
		for _, list := range result {
			// cluster they created, can modify
			if list.UserId == userId {
				list.Modifiable = true
				tempResult = append(tempResult, list)
				// cluster devSpace based on, can't modify
			} else if _, ok := clusterToUser[list.GetClusterId()]; ok {
				list.Modifiable = false
				tempResult = append(tempResult, list)
			}
		}
		result = tempResult[0:]
	} else {
		// administer have all privilege
		for _, list := range result {
			list.Modifiable = true
		}
	}
	vos := make([]model.ClusterListVo, len(result), len(result))
	var wg sync.WaitGroup
	wg.Add(len(result))
	for i, cluster := range result {
		if cluster == nil {
			wg.Done()
			continue
		}
		i := i
		go func() {
			vos[i] = model.ClusterListVo{
				ClusterList: *result[i],
				Resources:   GetResources(result[i].GetKubeConfig()),
			}
			wg.Done()
		}()
	}
	wg.Wait()
	api.SendResponse(c, errno.OK, vos)
}

// GetDevSpaceClusterList Get the devSpace cluster list
// @Summary Get the cluster list which user can create devSpace
// @Description Get the cluster list which user can create devSpace
// @Tags Cluster
// @Accept  json
// @Produce  json
// @param Authorization header string true "Authorization"
// @Success 200 {object} model.ClusterList "{"code":0,"message":"OK","data":model.ClusterList}"
// @Router /v2/dev_space/cluster [get]
func GetDevSpaceClusterList(c *gin.Context) {
	result, _ := service.Svc.ClusterSvc().GetList(c)
	tempResult := make([]*model.ClusterList, 0, 0)
	userId := c.GetUint64("userId")
	// normal user can only see clusters they created, or devSpace's cluster
	if isAdmin, _ := middleware.IsAdmin(c); !isAdmin {
		for _, list := range result {
			// devSpace cluster can be listed which created by normal user
			if list.UserId == userId {
				tempResult = append(tempResult, list)
			}
		}
		result = tempResult[0:]
	}
	api.SendResponse(c, errno.OK, result)
}

// resourceCache for cache resources(like cpu, memory, storage, pod number...), init cache with expire 15 seconds
var resourceCache = cache.NewCache(time.Second * 15)
var defaultValue = []model.Resource{
	{ResourceName: v1.ResourcePods, Capacity: 0, Used: 0, Percentage: 0},
	{ResourceName: v1.ResourceCPU, Capacity: 0, Used: 0, Percentage: 0},
	{ResourceName: v1.ResourceMemory, Capacity: 0, Used: 0, Percentage: 0},
	{ResourceName: v1.ResourceStorage, Capacity: 0, Used: 0, Percentage: 0},
}

// GetResources info by using metrics-api
func GetResources(kubeconfig string) []model.Resource {
	get, found := resourceCache.Get(kubeconfig)
	if found && get != nil {
		return get.([]model.Resource)
	}
	goclient, err := clientgo.NewAdminGoClientWithTimeout([]byte(kubeconfig), time.Second*2)
	if err != nil {
		return defaultValue
	}
	restClient, err := goclient.GetRestClient()
	if err != nil {
		return defaultValue
	}
	list, err := goclient.GetClusterNode()
	if err != nil {
		return defaultValue
	}
	summaries := make([]model.Summary, len(list.Items), len(list.Items))
	wg := sync.WaitGroup{}
	wg.Add(len(list.Items))
	for i, node := range list.Items {
		node := node
		i := i
		go func() {
			// using metrics-api to get nodes stats summary
			defer wg.Done()
			c := make(chan struct{}, 1)
			go func() {
				stream, _ := restClient.Get().
					Timeout(time.Second * 3).
					RequestURI(fmt.Sprintf("/api/v1/nodes/%s/proxy/stats/summary", node.Name)).
					DoRaw(context.Background())
				var s model.Summary
				_ = json.Unmarshal(stream, &s)
				summaries[i] = s
				c <- struct{}{}
			}()
			select {
			case <-c:
			case <-time.After(time.Second * 3):
				log.Warnf("get cluster info timeout")
			}
		}()
	}
	wg.Wait()
	summaries = summaries[0:]

	var cpuTotal, memoryTotal, storageTotal, podTotal int64
	var cpuUsed, memoryUsed, storageUsed, podUsed int64
	for _, node := range list.Items {
		cpuTotal += node.Status.Capacity.Cpu().MilliValue()
		// convert bytes to mega bytes (B --> MB)
		memoryTotal += node.Status.Capacity.Memory().Value() / 1024 / 1024
		storageTotal += node.Status.Capacity.StorageEphemeral().Value() / 1024 / 1024
		podTotal += node.Status.Capacity.Pods().Value()
	}
	for _, summary := range summaries {
		cpuUsed += resource.NewScaledQuantity(int64(summary.Node.CPU.UsageNanoCores), resource.Nano).
			ScaledValue(resource.Milli)
		// convert bytes to mega bytes (B --> MB)
		memoryUsed += int64(summary.Node.Memory.UsageBytes) / 1024 / 1024
		storageUsed += int64(summary.Node.Fs.UsedBytes) / 1024 / 1024
		podUsed += int64(len(summary.Pods))
	}

	resources := make([]model.Resource, 0, 4)
	resources = append(resources, model.Resource{
		ResourceName: v1.ResourcePods,
		Capacity:     float64(podTotal),
		Used:         float64(podUsed),
		Percentage:   Div(float64(podUsed), float64(podTotal)),
	}, model.Resource{
		ResourceName: v1.ResourceCPU,
		Capacity:     Div(float64(cpuTotal), 1000),
		Used:         Div(float64(cpuUsed), 1000),
		Percentage:   Div(float64(cpuUsed), float64(cpuTotal)),
	}, model.Resource{
		ResourceName: v1.ResourceMemory,
		Capacity:     Div(float64(memoryTotal), 1024),
		Used:         Div(float64(memoryUsed), 1024),
		Percentage:   Div(float64(memoryUsed), float64(memoryTotal)),
	}, model.Resource{
		ResourceName: v1.ResourceStorage,
		Capacity:     Div(float64(storageTotal), 1024),
		Used:         Div(float64(storageUsed), 1024),
		Percentage:   Div(float64(storageUsed), float64(storageTotal)),
	})
	resourceCache.Set(kubeconfig, resources)
	return resources
}

func Div(a float64, b float64) float64 {
	if b == 0 {
		b = 1
	}
	if float, err := strconv.ParseFloat(fmt.Sprintf("%.2f", a/b), 64); err == nil {
		return float
	}
	return 0
}

// list permitted dev_space by user
// distinct by cluster id
func ListByUser(c *gin.Context) {
	user := cast.ToUint64(c.Param("id"))
	result, _ := service.Svc.ClusterSvc().GetList(c)

	// user but admin can only access his own clusters
	if ginbase.IsAdmin(c) || ginbase.IsCurrentUser(c, user) {
		userModel := model.ClusterUserModel{
			UserId: user,
		}

		list, err := service.Svc.ClusterUser().GetList(c, userModel)
		if err != nil {
			api.SendResponse(c, errno.ErrClusterUserNotFound, nil)
		}

		set := map[uint64]interface{}{}
		for _, clusterUserModel := range list {
			set[clusterUserModel.ClusterId] = "-"
		}

		for _, cluster := range result {

			if _, ok := set[cluster.ID]; ok {
				cluster.HasDevSpace = true
			}
		}
	} else {
		api.SendResponse(c, errno.ErrLoginRequired, result)
	}

	api.SendResponse(c, errno.OK, result)
}

// @Summary Cluster dev space list
// @Description Cluster entrance to obtain cluster development environment
// @Tags Cluster
// @Accept  json
// @Produce  json
// @param Authorization header string true "Authorization"
// @Param id path string true "Cluster ID"
// @Success 200 {object} model.ClusterUserModel "kubeconfig"
// @Router /v1/cluster/{id}/dev_space [get]
func GetSpaceList(c *gin.Context) {
	//userId, _ := c.Get("userId")
	clusterId := cast.ToUint64(c.Param("id"))
	where := model.ClusterUserModel{
		ClusterId: clusterId,
	}
	result, err := service.Svc.ClusterUser().GetList(c, where)
	if err != nil {
		api.SendResponse(c, nil, make([]interface{}, 0))
		return
	}
	api.SendResponse(c, nil, result)
}

// @Summary Get cluster details
// @Description Get cluster details
// @Tags Cluster
// @Accept  json
// @Produce  json
// @param Authorization header string true "Authorization"
// @Param id path string true "Cluster ID"
// @Success 200 {object} model.ClusterModel "include kubeconfig"
// @Router /v1/cluster/{id}/detail [get]
func GetDetail(c *gin.Context) {
	// userId, _ := c.Get("userId")
	clusterId := cast.ToUint64(c.Param("id"))
	result, err := service.Svc.ClusterSvc().Get(c, clusterId)

	if err != nil {
		api.SendResponse(c, nil, make([]interface{}, 0))
		return
	}

	resp := ClusterDetailResponse{
		ID:           result.ID,
		Name:         result.Name,
		Info:         result.Info,
		UserId:       result.UserId,
		Server:       result.Server,
		KubeConfig:   "",
		StorageClass: result.StorageClass,
		CreatedAt:    result.CreatedAt,
	}

	// recreate
	//clusterDetail := model.ClusterDetailModel{
	//	ID:              result.ID,
	//	Name:            result.Name,
	//	Info:            result.Info,
	//	UserId:          result.UserId,
	//	Server:          result.Server,
	//	KubeConfig:      result.KubeConfig,
	//	CreatedAt:       result.CreatedAt,
	//	UpdatedAt:       result.UpdatedAt,
	//	DeletedAt:       result.DeletedAt,
	//	IsReady:         true,
	//	NotReadyMessage: "",
	//}
	//
	//// check cluster status
	//clientGo, err := clientgo.NewGoClient([]byte(result.KubeConfig))
	//if err != nil {
	//	clusterDetail.NotReadyMessage = "New go client fail"
	//	clusterDetail.IsReady = false
	//	api.SendResponse(c, nil, clusterDetail)
	//	return
	//}
	//_, err = clientGo.IfNocalhostNameSpaceExist()
	//if err != nil {
	//	clusterDetail.NotReadyMessage = "Can not get namespace: " + global.NocalhostSystemNamespace
	//	clusterDetail.IsReady = false
	//	api.SendResponse(c, nil, clusterDetail)
	//	return
	//}
	//err = clientGo.GetDepDeploymentStatus()
	//if err != nil {
	//	clusterDetail.NotReadyMessage = err.Error()
	//	clusterDetail.IsReady = false
	//	api.SendResponse(c, nil, clusterDetail)
	//	return
	//}

	api.SendResponse(c, errno.OK, resp)
}

// @Summary Details of a development environment in the cluster
// @Description Get cluster development environment details through cluster id and development environment id
// @Tags Cluster
// @Accept  json
// @Produce  json
// @param Authorization header string true "Authorization"
// @Param id path string true "Cluster ID"
// @Param space_id path string true "DevSpace ID"
// @Success 200 {object} model.ClusterUserModel "include kubeconfig"
// @Router /v1/cluster/{id}/dev_space/{space_id}/detail [get]
func GetSpaceDetail(c *gin.Context) {
	//userId, _ := c.Get("userId")
	clusterId := cast.ToUint64(c.Param("id"))
	devSpaceId := cast.ToUint64(c.Param("space_id"))
	where := model.ClusterUserModel{
		ID:        devSpaceId,
		ClusterId: clusterId,
	}
	result, err := service.Svc.ClusterUser().GetFirst(c, where)
	if err != nil {
		api.SendResponse(c, nil, nil)
		return
	}
	api.SendResponse(c, nil, result)
}

// @Summary Get cluster storageClass from cluster list
// @Description Get cluster storageClass from cluster list
// @Tags Cluster
// @Accept  json
// @Produce  json
// @param Authorization header string true "Authorization"
// @Param id path string true "Cluster ID"
// @Success 200 {object} cluster.StorageClassResponse "include kubeconfig"
// @Router /v1/cluster/{id}/storage_class [get]
func GetStorageClass(c *gin.Context) {
	// userId, _ := c.Get("userId")
	clusterKey := c.Param("id")
	var kubeConfig []byte
	if clusterKey == "kubeconfig" {
		var req StorageClassRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			api.SendResponse(c, errno.ErrBind, nil)
			return
		}
		if req.KubeConfig == "" {
			api.SendResponse(c, errno.ErrParam, nil)
			return
		}
		var err error
		if req.KubeConfig != "" {
			kubeConfig, err = base64.StdEncoding.DecodeString(req.KubeConfig)
			if err != nil {
				api.SendResponse(c, errno.ErrClusterKubeErr, nil)
				return
			}
		}
	} else {
		cluster, err := service.Svc.ClusterSvc().Get(c, cast.ToUint64(clusterKey))
		if err != nil {
			api.SendResponse(c, errno.ErrClusterNotFound, nil)
			return
		}
		kubeConfig = []byte(cluster.KubeConfig)
	}

	// new client go
	clientGo, err := clientgo.NewAdminGoClient(kubeConfig)

	// get client go and check if is admin Kubeconfig
	if err != nil {
		switch err.(type) {
		case *errno.Errno:
			api.SendResponse(c, err, nil)
		default:
			api.SendResponse(c, errno.ErrClusterKubeErr, nil)
		}
		return
	}
	storageClassList, err := clientGo.GetStorageClassList()
	if err != nil {
		api.SendResponse(c, errno.ErrGetClusterStorageClass, nil)
		return
	}
	var typeName []string
	for _, st := range storageClassList.Items {
		typeName = append(typeName, st.Name)
	}
	response := StorageClassResponse{
		TypeName: typeName,
	}
	api.SendResponse(c, nil, response)
	return
}

// @Summary Get cluster storageClass from create cluster
// @Description Get cluster storageClass from create cluster
// @Tags Cluster
// @Accept  json
// @Produce  json
// @param Authorization header string true "Authorization"
// @Param createCluster body cluster.StorageClassRequest true "The cluster info"
// @Success 200 {object} cluster.StorageClassResponse "include kubeconfig"
// @Router /v1/cluster/kubeconfig/storage_class [post]
func GetStorageClassByKubeConfig(c *gin.Context) {
	GetStorageClass(c)
}

// GenNamespace
// @Summary Gen Namespace
// @Description gen namespace for mesh dev space
// @Tags Cluster
// @Accept  json
// @Produce  json
// @param Authorization header string true "Authorization"
// @Param cluster path string true "cluster id"
// @Success 200 {object} cluster.Namespace "{"code":0,"message":"OK","data":cluster.Namespace}"
// @Router /v1/cluster/{id}/gen_namespace [get]
func GenNamespace(c *gin.Context) {
	cluster, err := service.Svc.ClusterSvc().Get(c, cast.ToUint64(c.Param("id")))
	if err != nil {
		api.SendResponse(c, errno.ErrClusterNotFound, nil)
		return
	}
	cluster.GetKubeConfig()
	client, err := clientgo.NewAdminGoClient([]byte(cluster.GetKubeConfig()))
	if err != nil {
		api.SendResponse(c, errno.ErrClusterKubeErr, nil)
		return
	}
	user, err := ginbase.LoginUser(c)
	if err != nil {
		api.SendResponse(c, errno.ErrUserNotFound, nil)
		return
	}

	var devNamespace string
	if err = wait.Poll(200*time.Millisecond, time.Second, func() (bool, error) {
		devNamespace = client.GenerateNsName(user)
		ok, err := client.IsNamespaceExist(devNamespace)
		return !ok, err
	}); err != nil {
		log.Error(err)
		api.SendResponse(c, errno.ErrClusterGenNamespace, nil)
	}

	api.SendResponse(c, nil, Namespace{devNamespace})
}
