package kube

type Group int

const (
	GroupWorkloads Group = iota
	GroupCluster
	GroupTraffic
	GroupStorage
)

var groupNames = map[Group]string{
	GroupWorkloads: "Workloads",
	GroupCluster:   "Cluster",
	GroupTraffic:   "Traffic",
	GroupStorage:   "Storage",
}

func Groups() []Group {
	return []Group{GroupWorkloads, GroupCluster, GroupTraffic, GroupStorage}
}

func (g Group) String() string {
	return groupNames[g]
}

type Kind int

const (
	KindPod Kind = iota
	KindDeployment
	KindReplicaSet
	KindDaemonSet
	KindStatefulSet
	KindNode
	KindNamespace
	KindResourceQuota
	KindLimitRange
	KindService
	KindEndpointSlice
	KindIngress
	KindNetworkPolicy
	KindHTTPRoute
	KindGateway
	KindVolumeClaim
	KindVolume
	KindStorageClass
)

type kindInfo struct {
	name       string
	column     string
	spoken     string
	group      Group
	namespaced bool
}

var kinds = map[Kind]kindInfo{
	KindPod:           {"Pods", "POD", "pod", GroupWorkloads, true},
	KindDeployment:    {"Deployments", "DEPLOYMENT", "deployment", GroupWorkloads, true},
	KindReplicaSet:    {"ReplicaSets", "REPLICASET", "replica set", GroupWorkloads, true},
	KindDaemonSet:     {"DaemonSets", "DAEMONSET", "daemon set", GroupWorkloads, true},
	KindStatefulSet:   {"StatefulSets", "STATEFULSET", "stateful set", GroupWorkloads, true},
	KindNode:          {"Nodes", "NODE", "node", GroupCluster, false},
	KindNamespace:     {"Namespaces", "NAMESPACE", "namespace", GroupCluster, false},
	KindResourceQuota: {"ResourceQuotas", "RESOURCEQUOTA", "resource quota", GroupCluster, true},
	KindLimitRange:    {"LimitRanges", "LIMITRANGE", "limit range", GroupCluster, true},
	KindService:       {"Services", "SERVICE", "service", GroupTraffic, true},
	KindEndpointSlice: {"EndpointSlices", "ENDPOINTSLICE", "endpoint slice", GroupTraffic, true},
	KindIngress:       {"Ingresses", "INGRESS", "ingress", GroupTraffic, true},
	KindNetworkPolicy: {"NetworkPolicies", "NETWORKPOLICY", "network policy", GroupTraffic, true},
	KindHTTPRoute:     {"HTTPRoutes", "HTTPROUTE", "HTTP route", GroupTraffic, true},
	KindGateway:       {"Gateways", "GATEWAY", "gateway", GroupTraffic, true},
	KindVolumeClaim:   {"VolumeClaims", "VOLUMECLAIM", "volume claim", GroupStorage, true},
	KindVolume:        {"Volumes", "VOLUME", "volume", GroupStorage, false},
	KindStorageClass:  {"StorageClasses", "STORAGECLASS", "storage class", GroupStorage, false},
}

var kindOrder = []Kind{
	KindPod, KindDeployment, KindReplicaSet, KindDaemonSet, KindStatefulSet,
	KindNode, KindNamespace, KindResourceQuota, KindLimitRange,
	KindService, KindEndpointSlice, KindIngress, KindNetworkPolicy, KindGateway, KindHTTPRoute,
	KindVolumeClaim, KindVolume, KindStorageClass,
}

func Kinds() []Kind {
	return kindOrder
}

func KindsIn(group Group) []Kind {
	out := make([]Kind, 0, len(kindOrder))
	for _, kind := range kindOrder {
		if kinds[kind].group == group {
			out = append(out, kind)
		}
	}
	return out
}

func (k Kind) String() string {
	return kinds[k].name
}

func (k Kind) Column() string {
	return kinds[k].column
}

func (k Kind) Spoken(count int) string {
	word := kinds[k].spoken
	if count == 1 {
		return word
	}
	switch k {
	case KindIngress:
		return "ingresses"
	case KindNetworkPolicy:
		return "network policies"
	case KindStorageClass:
		return "storage classes"
	}
	return word + "s"
}

func (k Kind) Group() Group {
	return kinds[k].group
}

func (k Kind) Namespaced() bool {
	return kinds[k].namespaced
}

func (k Kind) Measures() bool {
	if k.Group() == GroupTraffic || k.Group() == GroupStorage {
		return false
	}
	switch k {
	case KindNamespace, KindLimitRange:
		return false
	}
	return true
}

func KindByName(name string) (Kind, bool) {
	for kind, info := range kinds {
		if info.name == name {
			return kind, true
		}
	}
	return KindPod, false
}
