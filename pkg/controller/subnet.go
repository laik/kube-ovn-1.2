package controller

import (
	"fmt"
	kubeovnv1 "github.com/alauda/kube-ovn/pkg/apis/kubeovn/v1"
	"github.com/alauda/kube-ovn/pkg/ovs"
	"github.com/alauda/kube-ovn/pkg/util"
	"github.com/juju/errors"
	"net"
	"reflect"
	"strconv"
	"strings"

	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

func (c *Controller) enqueueAddSubnet(obj interface{}) {
	if !c.isLeader() {
		return
	}
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	klog.V(3).Infof("enqueue add subnet %s", key)
	c.addOrUpdateSubnetQueue.Add(key)
}

func (c *Controller) enqueueDeleteSubnet(obj interface{}) {
	if !c.isLeader() {
		return
	}
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	klog.V(3).Infof("enqueue delete subnet %s", key)
	c.deleteSubnetQueue.Add(key)
	subnet := obj.(*kubeovnv1.Subnet)
	if subnet.Spec.GatewayType == kubeovnv1.GWCentralizedType {
		c.deleteRouteQueue.Add(subnet.Spec.CIDRBlock)
	}
}

func (c *Controller) enqueueUpdateSubnet(old, new interface{}) {
	if !c.isLeader() {
		return
	}
	oldSubnet := old.(*kubeovnv1.Subnet)
	newSubnet := new.(*kubeovnv1.Subnet)

	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(new); err != nil {
		utilruntime.HandleError(err)
		return
	}

	if !newSubnet.DeletionTimestamp.IsZero() && newSubnet.Status.UsingIPs == 0 {
		c.addOrUpdateSubnetQueue.Add(key)
		return
	}

	if oldSubnet.Spec.Private != newSubnet.Spec.Private ||
		!reflect.DeepEqual(oldSubnet.Spec.AllowSubnets, newSubnet.Spec.AllowSubnets) ||
		!reflect.DeepEqual(oldSubnet.Spec.Namespaces, newSubnet.Spec.Namespaces) ||
		oldSubnet.Spec.GatewayType != newSubnet.Spec.GatewayType ||
		oldSubnet.Spec.GatewayNode != newSubnet.Spec.GatewayNode ||
		!reflect.DeepEqual(oldSubnet.Spec.ExcludeIps, newSubnet.Spec.ExcludeIps) ||
		!reflect.DeepEqual(oldSubnet.Spec.Vlan, newSubnet.Spec.Vlan) {
		klog.V(3).Infof("enqueue update subnet %s", key)
		c.addOrUpdateSubnetQueue.Add(key)
	}
}

func (c *Controller) runAddSubnetWorker() {
	for c.processNextAddSubnetWorkItem() {
	}
}

func (c *Controller) runUpdateSubnetStatusWorker() {
	for c.processNextUpdateSubnetStatusWorkItem() {
	}
}

func (c *Controller) runDeleteRouteWorker() {
	for c.processNextDeleteRoutePodWorkItem() {

	}
}

func (c *Controller) runDeleteSubnetWorker() {
	for c.processNextDeleteSubnetWorkItem() {
	}
}

func (c *Controller) processNextAddSubnetWorkItem() bool {
	obj, shutdown := c.addOrUpdateSubnetQueue.Get()
	if shutdown {
		return false
	}

	err := func(obj interface{}) error {
		defer c.addOrUpdateSubnetQueue.Done(obj)
		var key string
		var ok bool
		if key, ok = obj.(string); !ok {
			c.addOrUpdateSubnetQueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		if err := c.handleAddOrUpdateSubnet(key); err != nil {
			c.addOrUpdateSubnetQueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		c.addOrUpdateSubnetQueue.Forget(obj)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}
	return true
}

func (c *Controller) processNextDeleteRoutePodWorkItem() bool {
	obj, shutdown := c.deleteRouteQueue.Get()
	if shutdown {
		return false
	}

	err := func(obj interface{}) error {
		defer c.deleteRouteQueue.Done(obj)
		var key string
		var ok bool
		if key, ok = obj.(string); !ok {
			c.deleteRouteQueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		if err := c.handleDeleteRoute(key); err != nil {
			c.deleteRouteQueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		c.deleteRouteQueue.Forget(obj)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}
	return true
}

func (c *Controller) processNextUpdateSubnetStatusWorkItem() bool {
	obj, shutdown := c.updateSubnetStatusQueue.Get()
	if shutdown {
		return false
	}

	err := func(obj interface{}) error {
		defer c.updateSubnetStatusQueue.Done(obj)
		var key string
		var ok bool
		if key, ok = obj.(string); !ok {
			c.updateSubnetStatusQueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		if err := c.handleUpdateSubnetStatus(key); err != nil {
			c.updateSubnetStatusQueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		c.updateSubnetStatusQueue.Forget(obj)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}
	return true
}

func (c *Controller) processNextDeleteSubnetWorkItem() bool {
	obj, shutdown := c.deleteSubnetQueue.Get()
	if shutdown {
		return false
	}

	err := func(obj interface{}) error {
		defer c.deleteSubnetQueue.Done(obj)
		var key string
		var ok bool
		if key, ok = obj.(string); !ok {
			c.deleteSubnetQueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		if err := c.handleDeleteSubnet(key); err != nil {
			c.deleteSubnetQueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		c.deleteSubnetQueue.Forget(obj)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}
	return true
}

func formatSubnet(subnet *kubeovnv1.Subnet, c *Controller) error {
	var err error
	changed := false
	_, ipNet, err := net.ParseCIDR(subnet.Spec.CIDRBlock)
	if err != nil {
		return fmt.Errorf("subnet %s cidr %s is not a valid cidrblock", subnet.Name, subnet.Spec.CIDRBlock)
	}
	if ipNet.String() != subnet.Spec.CIDRBlock {
		subnet.Spec.CIDRBlock = ipNet.String()
		changed = true
	}
	if subnet.Spec.Provider == "" {
		subnet.Spec.Provider = util.OvnProvider
		changed = true
	}
	if subnet.Spec.Protocol == "" || subnet.Spec.Protocol != util.CheckProtocol(subnet.Spec.CIDRBlock) {
		subnet.Spec.Protocol = util.CheckProtocol(subnet.Spec.CIDRBlock)
		changed = true
	}
	if subnet.Spec.GatewayType == "" {
		subnet.Spec.GatewayType = kubeovnv1.GWDistributedType
		changed = true
	}
	if subnet.Spec.Default && subnet.Name != c.config.DefaultLogicalSwitch {
		subnet.Spec.Default = false
		changed = true
	}
	if subnet.Spec.Gateway == "" {
		gw, err := util.FirstSubnetIP(subnet.Spec.CIDRBlock)
		if err != nil {
			klog.Error(err)
			return err
		}
		subnet.Spec.Gateway = gw
		changed = true
	}

	if len(subnet.Spec.ExcludeIps) == 0 {
		subnet.Spec.ExcludeIps = []string{subnet.Spec.Gateway}
		changed = true
	} else {
		gwExists := false
		for _, ip := range ovs.ExpandExcludeIPs(subnet.Spec.ExcludeIps, subnet.Spec.CIDRBlock) {
			if ip == subnet.Spec.Gateway {
				gwExists = true
				break
			}
		}
		if !gwExists {
			subnet.Spec.ExcludeIps = append(subnet.Spec.ExcludeIps, subnet.Spec.Gateway)
			changed = true
		}
	}

	if c.config.NetworkType == util.NetworkTypeVlan && subnet.Spec.Vlan == "" {
		subnet.Spec.Vlan = c.config.DefaultVlanName
		changed = true
	}

	if subnet.Spec.Vlan != "" {
		if _, err := c.vlansLister.Get(subnet.Spec.Vlan); err != nil {
			subnet.Spec.Vlan = ""
			changed = true
		}
	}

	if changed {
		_, err = c.config.KubeOvnClient.KubeovnV1().Subnets().Update(subnet)
		if err != nil {
			klog.Errorf("failed to update subnet %s, %v", subnet.Name, err)
			return err
		}
	}
	return nil
}

func (c *Controller) handleSubnetFinalizer(subnet *kubeovnv1.Subnet) (bool, error) {
	if subnet.DeletionTimestamp.IsZero() && !util.ContainsString(subnet.Finalizers, util.ControllerName) {
		subnet.Finalizers = append(subnet.Finalizers, util.ControllerName)
		if _, err := c.config.KubeOvnClient.KubeovnV1().Subnets().Update(subnet); err != nil {
			klog.Errorf("failed to add finalizer to subnet %s, %v", subnet.Name, err)
			return false, err
		}
		return false, nil
	}

	if !subnet.DeletionTimestamp.IsZero() && subnet.Status.UsingIPs == 0 {
		subnet.Finalizers = util.RemoveString(subnet.Finalizers, util.ControllerName)
		if _, err := c.config.KubeOvnClient.KubeovnV1().Subnets().Update(subnet); err != nil {
			klog.Errorf("failed to remove finalizer from subnet %s, %v", subnet.Name, err)
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func (c Controller) patchSubnetStatus(subnet *kubeovnv1.Subnet, reason string, errStr string) {
	if errStr != "" {
		subnet.Status.SetError(reason, errStr)
		subnet.Status.NotValidated(reason, errStr)
		subnet.Status.NotReady(reason, errStr)
		c.recorder.Eventf(subnet, v1.EventTypeWarning, reason, errStr)
	} else {
		subnet.Status.Validated(reason, "")
		if reason == "SetPrivateLogicalSwitchSuccess" || reason == "ResetLogicalSwitchAclSuccess" {
			subnet.Status.Ready(reason, "")
		}
	}

	bytes, err := subnet.Status.Bytes()
	if err != nil {
		klog.Error(err)
	} else {
		if _, err := c.config.KubeOvnClient.KubeovnV1().Subnets().Patch(subnet.Name, types.MergePatchType, bytes, "status"); err != nil {
			klog.Error("patch subnet status failed", err)
		}
	}
}

func (c *Controller) handleAddOrUpdateSubnet(key string) error {
	subnet, err := c.subnetsLister.Get(key)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	deleted, err := c.handleSubnetFinalizer(subnet)
	if err != nil {
		klog.Errorf("handle subnet finalizer failed %v", err)
		return err
	}
	if deleted {
		return nil
	}

	if err := formatSubnet(subnet, c); err != nil {
		return err
	}

	if err := calcSubnetStatusIP(subnet, c); err != nil {
		klog.Errorf("calculate subnet %s used ip failed, %v", subnet.Name, err)
		return err
	}

	if err := c.ipam.AddOrUpdateSubnet(subnet.Name, subnet.Spec.CIDRBlock, subnet.Spec.ExcludeIps); err != nil {
		return err
	}

	if !isOvnSubnet(subnet) {
		return nil
	}

	if err = util.ValidateSubnet(*subnet); err != nil {
		klog.Errorf("failed to validate subnet %s, %v", subnet.Name, err)
		c.patchSubnetStatus(subnet, "ValidateLogicalSwitchFailed", err.Error())
		return err
	} else {
		c.patchSubnetStatus(subnet, "ValidateLogicalSwitchSuccess", "")
	}

	subnetList, err := c.subnetsLister.List(labels.Everything())
	if err != nil {
		klog.Errorf("failed to list subnets %v", err)
		return err
	}
	for _, sub := range subnetList {
		if sub.Name != subnet.Name && util.CIDRConflict(sub.Spec.CIDRBlock, subnet.Spec.CIDRBlock) {
			err = fmt.Errorf("subnet %s cidr %s conflict with subnet %s cidr %s", subnet.Name, subnet.Spec.CIDRBlock, sub.Name, sub.Spec.CIDRBlock)
			klog.Error(err)
			c.patchSubnetStatus(subnet, "ValidateLogicalSwitchFailed", err.Error())
			return err
		}
	}

	nodes, err := c.nodesLister.List(labels.Everything())
	if err != nil {
		klog.Errorf("failed to list nodes %v", err)
		return err
	}
	for _, node := range nodes {
		for _, addr := range node.Status.Addresses {
			if addr.Type == v1.NodeInternalIP && util.CIDRContainIP(subnet.Spec.CIDRBlock, addr.Address) {
				err = fmt.Errorf("subnet %s cidr %s conflict with node %s address %s", subnet.Name, subnet.Spec.CIDRBlock, node.Name, addr.Address)
				klog.Error(err)
				c.patchSubnetStatus(subnet, "ValidateLogicalSwitchFailed", err.Error())
				return err
			}
		}
	}

	exist, err := c.ovnClient.LogicalSwitchExists(subnet.Name)
	if err != nil {
		klog.Errorf("failed to list logical switch, %v", err)
		c.patchSubnetStatus(subnet, "ListLogicalSwitchFailed", err.Error())
		return err
	}

	if !exist {
		subnet.Status.EnsureStandardConditions()
		// If multiple namespace use same ls name, only first one will success
		if err := c.ovnClient.CreateLogicalSwitch(subnet.Name, subnet.Spec.Protocol, subnet.Spec.CIDRBlock, subnet.Spec.Gateway, subnet.Spec.ExcludeIps, subnet.Spec.UnderlayGateway); err != nil {
			c.patchSubnetStatus(subnet, "CreateLogicalSwitchFailed", err.Error())
			return err
		}
	} else {
		// logical switch exists, only update other_config
		if err := c.ovnClient.SetLogicalSwitchConfig(subnet.Name, subnet.Spec.Protocol, subnet.Spec.CIDRBlock, subnet.Spec.Gateway, subnet.Spec.ExcludeIps); err != nil {
			c.patchSubnetStatus(subnet, "SetLogicalSwitchConfigFailed", err.Error())
			return err
		}
	}

	if err := c.reconcileSubnet(subnet); err != nil {
		klog.Errorf("reconcile subnet for %s failed, %v", subnet.Name, err)
		return err
	}

	if subnet.Spec.Private {
		if err := c.ovnClient.SetPrivateLogicalSwitch(subnet.Name, subnet.Spec.Protocol, subnet.Spec.CIDRBlock, subnet.Spec.AllowSubnets); err != nil {
			c.patchSubnetStatus(subnet, "SetPrivateLogicalSwitchFailed", err.Error())
			return err
		} else {
			c.patchSubnetStatus(subnet, "SetPrivateLogicalSwitchSuccess", "")
		}
	} else {
		if err := c.ovnClient.ResetLogicalSwitchAcl(subnet.Name, subnet.Spec.Protocol); err != nil {
			c.patchSubnetStatus(subnet, "ResetLogicalSwitchAclFailed", err.Error())
			return err
		} else {
			c.patchSubnetStatus(subnet, "ResetLogicalSwitchAclSuccess", "")
		}
	}

	return nil
}

func (c *Controller) handleUpdateSubnetStatus(key string) error {
	subnet, err := c.subnetsLister.Get(key)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return calcSubnetStatusIP(subnet, c)
}

func (c *Controller) handleDeleteRoute(key string) error {
	if _, _, err := net.ParseCIDR(key); err != nil {
		return nil
	}

	return c.ovnClient.DeleteStaticRoute(key, c.config.ClusterRouter)
}

func (c *Controller) handleDeleteSubnet(key string) error {
	c.ipam.DeleteSubnet(key)

	exist, err := c.ovnClient.LogicalSwitchExists(key)
	if err != nil {
		klog.Errorf("failed to list logical switch, %v", err)
		return err
	}
	if !exist {
		return nil
	}

	if err = c.ovnClient.CleanLogicalSwitchAcl(key); err != nil {
		klog.Errorf("failed to delete acl of logical switch %s %v", key, err)
		return err
	}
	if err = c.ovnClient.DeleteLogicalSwitch(key); err != nil {
		klog.Errorf("failed to delete logical switch %s %v", key, err)
		return err
	}

	nss, err := c.namespacesLister.List(labels.Everything())
	if err != nil {
		klog.Errorf("failed to list namespaces, %v", err)
		return err
	}

	// re-annotate namespace
	for _, ns := range nss {
		annotations := ns.GetAnnotations()
		if annotations == nil {
			continue
		}
		if annotations[util.LogicalSwitchAnnotation] == key {
			c.enqueueAddNamespace(ns)
		}
	}

	// re-annotate vlan subnet
	if c.config.NetworkType == util.NetworkTypeVlan {
		if err = c.delLocalnet(key); err != nil {
			return err
		}

		vlans, err := c.vlansLister.List(labels.Everything())
		if err != nil {
			klog.Errorf("failed to list vlan, %v", err)
			return err
		}

		for _, vlan := range vlans {
			subnet := strings.Split(vlan.Spec.Subnet, ",")
			if util.IsStringIn(key, subnet) {
				c.updateVlanQueue.Add(vlan.Name)
			}
		}
	}

	return nil
}

func (c *Controller) reconcileSubnet(subnet *kubeovnv1.Subnet) error {
	if err := c.reconcileNamespaces(subnet); err != nil {
		klog.Errorf("reconcile namespaces for subnet %s failed, %v", subnet.Name, err)
		return err
	}

	if subnet.Name != c.config.NodeSwitch {
		if err := c.reconcileGateway(subnet); err != nil {
			klog.Errorf("reconcile centralized gateway for subnet %s failed, %v", subnet.Name, err)
			return err
		}
	}

	if err := c.reconcileVlan(subnet); err != nil {
		klog.Errorf("reconcile vlan for subnet %s failed, %v", subnet.Name, err)
		return err
	}
	return nil
}

func (c *Controller) reconcileNamespaces(subnet *kubeovnv1.Subnet) error {
	var err error
	// 1. unbind from previous subnet
	subnets, err := c.subnetsLister.List(labels.Everything())
	if err != nil {
		return err
	}

	namespaceMap := map[string]bool{}
	for _, ns := range subnet.Spec.Namespaces {
		namespaceMap[ns] = true
	}

	for _, sub := range subnets {
		if sub.Name == subnet.Name || len(sub.Spec.Namespaces) == 0 {
			continue
		}

		changed := false
		reservedNamespaces := []string{}
		for _, ns := range sub.Spec.Namespaces {
			if namespaceMap[ns] {
				changed = true
			} else {
				reservedNamespaces = append(reservedNamespaces, ns)
			}
		}
		if changed {
			sub.Spec.Namespaces = reservedNamespaces
			subnet, err = c.config.KubeOvnClient.KubeovnV1().Subnets().Update(sub)
			if err != nil {
				klog.Errorf("failed to unbind namespace from subnet %s, %v", sub.Name, err)
				return err
			}
		}
	}

	// 2. add annotations to bind namespace
	for _, ns := range subnet.Spec.Namespaces {
		c.addNamespaceQueue.Add(ns)
	}

	// 3. update unbind namespace annotation
	namespaces, err := c.namespacesLister.List(labels.Everything())
	if err != nil {
		klog.Errorf("failed to list namespaces, %v", err)
		return err
	}

	for _, ns := range namespaces {
		if ns.Annotations != nil && ns.Annotations[util.LogicalSwitchAnnotation] == subnet.Name && !namespaceMap[ns.Name] {
			c.addNamespaceQueue.Add(ns.Name)
		}
	}

	return nil
}

func (c *Controller) reconcileGateway(subnet *kubeovnv1.Subnet) error {
	ips, err := c.config.KubeOvnClient.KubeovnV1().IPs().List(metav1.ListOptions{LabelSelector: fmt.Sprintf("%s=%s", util.SubnetNameLabel, subnet.Name)})
	if err != nil {
		klog.Errorf("failed to list ip of subnet %s, %v", subnet.Name, err)
		return err
	}

	// if gw is distributed remove activateGateway field
	if subnet.Spec.GatewayType == kubeovnv1.GWDistributedType {
		if subnet.Status.ActivateGateway == "" {
			return nil
		}
		subnet.Status.ActivateGateway = ""
		bytes, err := subnet.Status.Bytes()
		if err != nil {
			return err
		}
		_, err = c.config.KubeOvnClient.KubeovnV1().Subnets().Patch(subnet.Name, types.MergePatchType, bytes, "status")
		if err != nil {
			return err
		}

		for _, ip := range ips.Items {
			node, err := c.nodesLister.Get(ip.Spec.NodeName)
			if err != nil {
				if k8serrors.IsNotFound(err) {
					continue
				} else {
					klog.Errorf("failed to get node %s, %v", ip.Spec.NodeName, err)
					return err
				}
			}
			nextHop, err := getNodeTunlIP(node)
			if err != nil {
				klog.Errorf("failed to get node %s tunl ip, %v", node.Name, err)
				return err
			}
			if err := c.ovnClient.AddStaticRoute(ovs.PolicySrcIP, ip.Spec.IPAddress, nextHop.String(), c.config.ClusterRouter); err != nil {
				return errors.Annotate(err, "add static route failed")
			}
		}
		if err := c.ovnClient.DeleteStaticRoute(subnet.Spec.CIDRBlock, c.config.ClusterRouter); err != nil {
			klog.Errorf("failed to delete static route %s, %v", subnet.Spec.CIDRBlock, err)
			return err
		}
		return nil
	}
	klog.Infof("start to init centralized gateway for subnet %s", subnet.Name)

	// check if activateGateway still ready
	if subnet.Status.ActivateGateway != "" {
		node, err := c.nodesLister.Get(subnet.Status.ActivateGateway)
		if err == nil && nodeReady(node) {
			klog.Infof("subnet %s uses the old activate gw %s", subnet.Name, node.Name)
			return nil
		}
	}

	klog.Info("find a new activate node")
	// need a new activate gateway
	newActivateNode := ""
	var nodeTunlIPAddr net.IP
	for _, gw := range strings.Split(subnet.Spec.GatewayNode, ",") {
		gw = strings.TrimSpace(gw)
		node, err := c.nodesLister.Get(gw)
		if err == nil && nodeReady(node) {
			newActivateNode = node.Name
			nodeTunlIPAddr, err = getNodeTunlIP(node)
			if err != nil {
				return err
			}
			klog.Infof("subnet %s uses a new activate gw %s", subnet.Name, node.Name)
			break
		}
	}
	if newActivateNode == "" {
		klog.Warningf("all subnet %s gws are not ready", subnet.Name)
		subnet.Status.ActivateGateway = newActivateNode
		subnet.Status.NotReady("NoReadyGateway", "")
		bytes, err := subnet.Status.Bytes()
		if err != nil {
			return err
		}
		_, err = c.config.KubeOvnClient.KubeovnV1().Subnets().Patch(subnet.Name, types.MergePatchType, bytes, "status")
		return err
	}

	if err := c.ovnClient.AddStaticRoute(ovs.PolicySrcIP, subnet.Spec.CIDRBlock, nodeTunlIPAddr.String(), c.config.ClusterRouter); err != nil {
		return errors.Annotate(err, "add static route failed")
	}
	for _, ip := range ips.Items {
		if err := c.ovnClient.DeleteStaticRoute(ip.Spec.IPAddress, c.config.ClusterRouter); err != nil {
			klog.Errorf("failed to delete static route, %v", err)
			return err
		}
	}

	subnet.Status.ActivateGateway = newActivateNode
	bytes, err := subnet.Status.Bytes()
	subnet.Status.Ready("ReconcileCentralizedGatewaySuccess", "")
	if err != nil {
		return err
	}
	_, err = c.config.KubeOvnClient.KubeovnV1().Subnets().Patch(subnet.Name, types.MergePatchType, bytes, "status")
	return err
}

func (c *Controller) reconcileVlan(subnet *kubeovnv1.Subnet) error {
	if c.config.NetworkType != util.NetworkTypeVlan {
		return nil
	}

	klog.Infof("reconcile vlan, %v", subnet.Spec.Vlan)

	if subnet.Spec.Vlan != "" {
		//create subnet localnet
		if err := c.addLocalnet(subnet); err != nil {
			klog.Errorf("failed add localnet to subnet, %v", err)
			return err
		}

		c.updateVlanQueue.Add(subnet.Spec.Vlan)
	}

	//update unbind vlan
	vlanLists, err := c.vlansLister.List(labels.Everything())
	if err != nil {
		klog.Errorf("failed to list vlans, %v", err)
		return err
	}

	for _, vlan := range vlanLists {
		subnets := strings.Split(vlan.Spec.Subnet, ",")
		if util.IsStringIn(subnet.Name, subnets) {
			c.updateVlanQueue.Add(vlan.Name)
		}
	}

	return nil
}

func calcSubnetStatusIP(subnet *kubeovnv1.Subnet, c *Controller) error {
	_, cidr, err := net.ParseCIDR(subnet.Spec.CIDRBlock)
	if err != nil {
		return err
	}
	podUsedIPs, err := c.config.KubeOvnClient.KubeovnV1().IPs().List(metav1.ListOptions{
		LabelSelector: fields.OneTermEqualSelector(subnet.Name, "").String(),
	})
	if err != nil {
		return err
	}
	// gateway always in excludeIPs
	toSubIPs := ovs.ExpandExcludeIPs(subnet.Spec.ExcludeIps, subnet.Spec.CIDRBlock)
	for _, podUsedIP := range podUsedIPs.Items {
		toSubIPs = append(toSubIPs, podUsedIP.Spec.IPAddress)
	}
	availableIPs := util.AddressCount(cidr) - float64(len(util.UniqString(toSubIPs)))
	usingIPs := float64(len(podUsedIPs.Items))
	subnet.Status.AvailableIPs = availableIPs
	subnet.Status.UsingIPs = usingIPs
	bytes, err := subnet.Status.Bytes()
	if err != nil {
		return err
	}
	subnet, err = c.config.KubeOvnClient.KubeovnV1().Subnets().Patch(subnet.Name, types.MergePatchType, bytes, "status")
	return err
}

func isOvnSubnet(subnet *kubeovnv1.Subnet) bool {
	if subnet.Spec.Provider == util.OvnProvider || subnet.Spec.Provider == "" {
		return true
	}
	return false
}

func (c *Controller) getSubnetVlanTag(subnet *kubeovnv1.Subnet) (string, error) {
	tag := ""
	if subnet.Spec.Vlan != "" {
		vlan, err := c.vlansLister.Get(subnet.Spec.Vlan)
		if err != nil {
			return "", err
		}
		tag = strconv.Itoa(vlan.Spec.VlanId)
	}
	return tag, nil
}
