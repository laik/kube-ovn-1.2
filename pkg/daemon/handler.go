package daemon

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/emicklei/go-restful"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"

	kubeovnv1 "github.com/alauda/kube-ovn/pkg/apis/kubeovn/v1"
	clientset "github.com/alauda/kube-ovn/pkg/client/clientset/versioned"
	"github.com/alauda/kube-ovn/pkg/request"
	"github.com/alauda/kube-ovn/pkg/util"
)

type cniServerHandler struct {
	Config        *Configuration
	KubeClient    kubernetes.Interface
	KubeOvnClient clientset.Interface
}

func createCniServerHandler(config *Configuration) *cniServerHandler {
	csh := &cniServerHandler{KubeClient: config.KubeClient, KubeOvnClient: config.KubeOvnClient, Config: config}
	return csh
}

func (csh cniServerHandler) handleAdd(req *restful.Request, resp *restful.Response) {
	podRequest := request.CniRequest{}
	if err := req.ReadEntity(&podRequest); err != nil {
		errMsg := fmt.Errorf("parse add request failed %v", err)
		klog.Error(errMsg)
		resp.WriteHeaderAndEntity(http.StatusBadRequest, request.CniResponse{Err: errMsg.Error()})
		return
	}

	klog.Infof("add port request %v", podRequest)
	var macAddr, ip, ipAddr, cidr, gw, subnet, ingress, egress, vlanID string
	var pod *v1.Pod
	var err error
	for i := 0; i < 15; i++ {
		pod, err = csh.KubeClient.CoreV1().Pods(podRequest.PodNamespace).Get(podRequest.PodName, metav1.GetOptions{})
		if err != nil {
			errMsg := fmt.Errorf("get pod %s/%s failed %v", podRequest.PodNamespace, podRequest.PodName, err)
			klog.Error(errMsg)
			resp.WriteHeaderAndEntity(http.StatusInternalServerError, request.CniResponse{Err: errMsg.Error()})
			return
		}
		if pod.Annotations[fmt.Sprintf(util.AllocatedAnnotationTemplate, podRequest.Provider)] != "true" {
			klog.Infof("wait address for  pod %s/%s ", podRequest.PodNamespace, podRequest.PodName)
			// wait controller assign an address
			time.Sleep(1 * time.Second)
			continue
		}

		if err := util.ValidatePodNetwork(pod.Annotations); err != nil {
			klog.Errorf("validate pod %s/%s failed, %v", podRequest.PodNamespace, podRequest.PodName, err)
			// wait controller assign an address
			time.Sleep(1 * time.Second)
			continue
		}
		macAddr = pod.Annotations[fmt.Sprintf(util.MacAddressAnnotationTemplate, podRequest.Provider)]
		ip = pod.Annotations[fmt.Sprintf(util.IpAddressAnnotationTemplate, podRequest.Provider)]
		cidr = pod.Annotations[fmt.Sprintf(util.CidrAnnotationTemplate, podRequest.Provider)]
		gw = pod.Annotations[fmt.Sprintf(util.GatewayAnnotationTemplate, podRequest.Provider)]
		subnet = pod.Annotations[fmt.Sprintf(util.LogicalSwitchAnnotationTemplate, podRequest.Provider)]
		ingress = pod.Annotations[util.IngressRateAnnotation]
		egress = pod.Annotations[util.EgressRateAnnotation]
		vlanID = pod.Annotations[util.VlanIdAnnotation]
		ipAddr = fmt.Sprintf("%s/%s", ip, strings.Split(cidr, "/")[1])
		break
	}

	if pod.Annotations[fmt.Sprintf(util.AllocatedAnnotationTemplate, podRequest.Provider)] != "true" {
		err := fmt.Errorf("no address allocated to pod %s/%s, please see kube-ovn-controller logs to find errors", pod.Name, pod.Name)
		klog.Error(err)
		resp.WriteHeaderAndEntity(http.StatusInternalServerError, request.CniResponse{Err: err.Error()})
		return
	}

	if err := csh.createOrUpdateIPCr(podRequest, subnet, ip, macAddr); err != nil {
		resp.WriteHeaderAndEntity(http.StatusInternalServerError, request.CniResponse{Err: err.Error()})
		return
	}

	if podRequest.Provider == util.OvnProvider {
		klog.Infof("create container mac %s, ip %s, cidr %s, gw %s", macAddr, ipAddr, cidr, gw)
		err := csh.configureNic(podRequest.PodName, podRequest.PodNamespace, podRequest.NetNs, podRequest.ContainerID, podRequest.IfName, macAddr, ipAddr, gw, ingress, egress, vlanID, podRequest.DeviceID)
		if err != nil {
			errMsg := fmt.Errorf("configure nic failed %v", err)
			klog.Error(errMsg)
			resp.WriteHeaderAndEntity(http.StatusInternalServerError, request.CniResponse{Err: errMsg.Error()})
			return
		}
	}

	resp.WriteHeaderAndEntity(http.StatusOK, request.CniResponse{Protocol: util.CheckProtocol(ipAddr), IpAddress: strings.Split(ipAddr, "/")[0], MacAddress: macAddr, CIDR: cidr, Gateway: gw})
}

func (csh cniServerHandler) createOrUpdateIPCr(podRequest request.CniRequest, subnet, ip, macAddr string) error {
	ipCr, err := csh.KubeOvnClient.KubeovnV1().IPs().Get(fmt.Sprintf("%s.%s", podRequest.PodName, podRequest.PodNamespace), metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			_, err := csh.KubeOvnClient.KubeovnV1().IPs().Create(&kubeovnv1.IP{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf("%s.%s", podRequest.PodName, podRequest.PodNamespace),
					Labels: map[string]string{
						util.SubnetNameLabel: subnet,
						subnet:               "",
					},
				},
				Spec: kubeovnv1.IPSpec{
					PodName:     podRequest.PodName,
					Namespace:   podRequest.PodNamespace,
					Subnet:      subnet,
					NodeName:    csh.Config.NodeName,
					IPAddress:   ip,
					MacAddress:  macAddr,
					ContainerID: podRequest.ContainerID,
				},
			})
			if err != nil {
				errMsg := fmt.Errorf("failed to create ip crd for %s, %v", ip, err)
				klog.Error(errMsg)
				return errMsg
			}
		} else {
			errMsg := fmt.Errorf("failed to get ip crd for %s, %v", ip, err)
			klog.Error(errMsg)
			return errMsg
		}
	} else {
		ipCr.Labels[subnet] = ""
		ipCr.Spec.AttachSubnets = append(ipCr.Spec.AttachSubnets, subnet)
		ipCr.Spec.AttachIPs = append(ipCr.Spec.AttachIPs, ip)
		ipCr.Spec.AttachMacs = append(ipCr.Spec.AttachMacs, macAddr)
		if _, err := csh.KubeOvnClient.KubeovnV1().IPs().Update(ipCr); err != nil {
			errMsg := fmt.Errorf("failed to update ip crd for %s, %v", ip, err)
			klog.Error(errMsg)
			return errMsg
		}
	}
	return nil
}

func (csh cniServerHandler) handleDel(req *restful.Request, resp *restful.Response) {
	podRequest := request.CniRequest{}
	err := req.ReadEntity(&podRequest)

	if err != nil {
		errMsg := fmt.Errorf("parse del request failed %v", err)
		klog.Error(errMsg)
		resp.WriteHeaderAndEntity(http.StatusBadRequest, request.CniResponse{Err: errMsg.Error()})
		return
	}

	klog.Infof("delete port request %v", podRequest)
	if podRequest.Provider == util.OvnProvider {
		err = csh.deleteNic(podRequest.PodName, podRequest.PodNamespace, podRequest.ContainerID, podRequest.DeviceID)
		if err != nil {
			errMsg := fmt.Errorf("del nic failed %v", err)
			klog.Error(errMsg)
			if err := resp.WriteHeaderAndEntity(http.StatusInternalServerError, request.CniResponse{Err: errMsg.Error()}); err != nil {
				klog.Errorf("failed to write response, %v", err)
			}
			return
		}
	}

	err = csh.KubeOvnClient.KubeovnV1().IPs().Delete(fmt.Sprintf("%s.%s", podRequest.PodName, podRequest.PodNamespace), &metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		errMsg := fmt.Errorf("del ipcrd for %s failed %v", fmt.Sprintf("%s.%s", podRequest.PodName, podRequest.PodNamespace), err)
		klog.Error(errMsg)
		resp.WriteHeaderAndEntity(http.StatusInternalServerError, request.CniResponse{Err: errMsg.Error()})
		return
	}

	resp.WriteHeader(http.StatusNoContent)
}
