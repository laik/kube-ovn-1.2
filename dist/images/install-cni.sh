#!/bin/bash

set -u -e

if [[ -f "/proc/sys/net/bridge/bridge-nf-call-iptables" ]];
    then echo 1 > /proc/sys/net/bridge/bridge-nf-call-iptables;
fi

if [[ -f "/proc/sys/net/ipv4/ip_forward" ]];
    then echo 1 > /proc/sys/net/ipv4/ip_forward;
fi

if [[ -f "/proc/sys/net/ipv6/conf/all/forwarding" ]];
    then echo 1 > /proc/sys/net/ipv6/conf/all/forwarding;
fi

if [[ -f "/proc/sys/net/ipv4/conf/all/rp_filter" ]];
    then echo 0 > /proc/sys/net/ipv4/conf/all/rp_filter;
fi

exit_with_error(){
  echo $1
  exit 1
}

CNI_BIN_SRC=/kube-ovn/kube-ovn
CNI_BIN_DST=/opt/cni/bin/kube-ovn

CNI_CONF_SRC=/kube-ovn/01-kube-ovn.conflist
CNI_CONF_DST=/etc/cni/net.d/01-kube-ovn.conflist

LOOPBACK_BIN_SRC=/loopback
LOOPBACK_BIN_DST=/opt/cni/bin/loopback

PORTMAP_BIN_SRC=/portmap
PORTMAP_BIN_DST=/opt/cni/bin/portmap

ROUTE_OVERRIDE_BIN_SRC=/route-override
ROUTE_OVERRIDE_BIN_DST=/opt/cni/bin/route-override

GLOBAL_IPAM_BIN_SRC=/global-ipam
GLOBAL_IPAM_BIN_DST=/opt/cni/bin/global-ipam


yes | cp -f $LOOPBACK_BIN_SRC $LOOPBACK_BIN_DST || exit_with_error "Failed to copy $LOOPBACK_BIN_SRC to $LOOPBACK_BIN_DST"
yes | cp -f $PORTMAP_BIN_SRC $PORTMAP_BIN_DST || exit_with_error "Failed to copy $PORTMAP_BIN_SRC to $PORTMAP_BIN_DST"
yes | cp -f $ROUTE_OVERRIDE_BIN_SRC $ROUTE_OVERRIDE_BIN_DST || exit_with_error "Failed to copy $ROUTE_OVERRIDE_BIN_SRC to $ROUTE_OVERRIDE_BIN_DST"
yes | cp -f $GLOBAL_IPAM_BIN_SRC $GLOBAL_IPAM_BIN_DST || exit_with_error "Failed to copy $GLOBAL_IPAM_BIN_SRC to $GLOBAL_IPAM_BIN_DST"
yes | cp -f $CNI_BIN_SRC $CNI_BIN_DST || exit_with_error "Failed to copy $CNI_BIN_SRC to $CNI_BIN_DST"
yes | cp -f $CNI_CONF_SRC $CNI_CONF_DST || exit_with_error "Failed to copy $CNI_CONF_SRC to $CNI_CONF_DST"
