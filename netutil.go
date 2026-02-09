package main

import (
	"encoding/binary"
	"fmt"
	"net"
)

func cidrFromSubnetAndGateway(subnet, gateway string) (string, error) {
	ipnet, err := parseCIDRv4(subnet)
	if err != nil {
		return "", fmt.Errorf("invalid subnet")
	}
	ip, err := parseIPv4(gateway)
	if err != nil {
		return "", fmt.Errorf("invalid gateway IP")
	}
	if !ipnet.Contains(ip) {
		return "", fmt.Errorf("gateway IP not in subnet")
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 {
		return "", fmt.Errorf("unsupported subnet")
	}
	return fmt.Sprintf("%s/%d", ip.String(), ones), nil
}

func subnetFromCIDR(cidr string) (string, error) {
	ipnet, err := parseCIDRv4(cidr)
	if err != nil {
		return "", err
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 {
		return "", fmt.Errorf("unsupported CIDR")
	}
	network := ipnet.IP.Mask(ipnet.Mask)
	return fmt.Sprintf("%s/%d", network.String(), ones), nil
}

func cidrFromAddrNetmask(address, netmask string) (string, error) {
	ip, err := parseIPv4(address)
	if err != nil {
		return "", fmt.Errorf("invalid address")
	}
	mask, err := parseNetmask(netmask)
	if err != nil {
		return "", err
	}
	ones, bits := mask.Size()
	if bits != 32 {
		return "", fmt.Errorf("invalid netmask")
	}
	return fmt.Sprintf("%s/%d", ip.String(), ones), nil
}

func parseIPv4(s string) (net.IP, error) {
	ip := net.ParseIP(s).To4()
	if ip == nil {
		return nil, fmt.Errorf("invalid IPv4 address")
	}
	return ip, nil
}

func parseCIDRv4(s string) (*net.IPNet, error) {
	ip, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		return nil, err
	}
	if ip.To4() == nil {
		return nil, fmt.Errorf("IPv4 CIDR required")
	}
	ipnet.IP = ip.To4()
	return ipnet, nil
}

func parseNetmask(netmask string) (net.IPMask, error) {
	maskIP := net.ParseIP(netmask).To4()
	if maskIP == nil {
		return nil, fmt.Errorf("invalid netmask")
	}
	mask := net.IPv4Mask(maskIP[0], maskIP[1], maskIP[2], maskIP[3])
	ones, bits := mask.Size()
	if bits != 32 {
		return nil, fmt.Errorf("invalid netmask")
	}
	if ones == 0 {
		return nil, fmt.Errorf("invalid netmask")
	}
	return mask, nil
}

func ipToUint32(ip net.IP) uint32 {
	return binary.BigEndian.Uint32(ip.To4())
}

func ipLessOrEqual(a, b net.IP) bool {
	return ipToUint32(a) <= ipToUint32(b)
}

func ipInNet(ip net.IP, ipnet *net.IPNet) bool {
	return ipnet.Contains(ip)
}

func validateDHCPRange(subnet, gateway, start, end string) error {
	ipnet, err := parseCIDRv4(subnet)
	if err != nil {
		return fmt.Errorf("bridge subnet invalid")
	}
	startIP, err := parseIPv4(start)
	if err != nil {
		return fmt.Errorf("invalid range start IP")
	}
	endIP, err := parseIPv4(end)
	if err != nil {
		return fmt.Errorf("invalid range end IP")
	}
	if !ipInNet(startIP, ipnet) || !ipInNet(endIP, ipnet) {
		return fmt.Errorf("DHCP range must be within bridge subnet")
	}
	if !ipLessOrEqual(startIP, endIP) {
		return fmt.Errorf("range start must be <= range end")
	}
	gwIP, err := parseIPv4(gateway)
	if err == nil {
		if ipLessOrEqual(startIP, gwIP) && ipLessOrEqual(gwIP, endIP) {
			return fmt.Errorf("DHCP range must not include gateway IP")
		}
	}
	return nil
}
