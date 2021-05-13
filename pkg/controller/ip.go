package controller

import (
	kubeovnv1 "github.com/alauda/kube-ovn/pkg/apis/kubeovn/v1"

	"k8s.io/klog"
)

func (c *Controller) enqueueAddOrDelIP(obj interface{}) {
	if !c.isLeader() {
		return
	}
	ipObj := obj.(*kubeovnv1.IP)
	klog.V(3).Infof("enqueue update status subnet %s", ipObj.Spec.Subnet)
	c.updateSubnetStatusQueue.Add(ipObj.Spec.Subnet)
	for _, as := range ipObj.Spec.AttachSubnets {
		klog.V(3).Infof("enqueue update status subnet %s", as)
		c.updateSubnetStatusQueue.Add(as)
	}
}

func (c *Controller) enqueueUpdateIP(old, new interface{}) {
	if !c.isLeader() {
		return
	}
	ipObj := new.(*kubeovnv1.IP)
	klog.V(3).Infof("enqueue update status subnet %s", ipObj.Spec.Subnet)
	for _, as := range ipObj.Spec.AttachSubnets {
		klog.V(3).Infof("enqueue update status subnet %s", as)
		c.updateSubnetStatusQueue.Add(as)
	}
}
