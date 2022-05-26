/**
 *  Copyright 2013 Rackspace
 *
 *  Licensed under the Apache License, Version 2.0 (the "License");
 *  you may not use this file except in compliance with the License.
 *  You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 *  Unless required by applicable law or agreed to in writing, software
 *  distributed under the License is distributed on an "AS IS" BASIS,
 *  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 *  See the License for the specific language governing permissions and
 *  limitations under the License.
 *
 *  Note: original copyright is preserved on purpose
 */

package multiplexer

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"unsafe"

	"github.com/gravitational/trace"
)

const (
	// TCP4 is TCP over IPv4
	TCP4 = "TCP4"
	// TCP6 is tCP over IPv6
	TCP6 = "TCP6"
	// Unknown is unsupported or unknown protocol
	UNKNOWN = "UNKNOWN"
)

var (
	proxyCRLF = "\r\n"
	proxySep  = " "
)

// ProxyLine is HA Proxy protocol version 1
// https://www.haproxy.org/download/1.8/doc/proxy-protocol.txt
// Original implementation here: https://github.com/racker/go-proxy-protocol
type ProxyLine struct {
	Protocol    string
	Source      net.TCPAddr
	Destination net.TCPAddr
}

// String returns on-the wire string representation of the proxy line
func (p *ProxyLine) String() string {
	return fmt.Sprintf("PROXY %s %s %s %d %d\r\n", p.Protocol, p.Source.IP.String(), p.Destination.IP.String(), p.Source.Port, p.Destination.Port)
}

// Bytes returns on-the wire bytes representation of proxy line conforming to the proxy v2 protocol
func (p *ProxyLine) Bytes() []byte {
	b := &bytes.Buffer{}
	header := proxyV2Header{VersionCommand: (Version2 << 4) | ProxyCommand}
	copy(header.Signature[:], proxyV2Prefix)
	var addr interface{}
	switch p.Protocol {
	case TCP4:
		header.Protocol = ProtocolTCP4
		addr4 := proxyV2Address4{
			SourcePort:      uint16(p.Source.Port),
			DestinationPort: uint16(p.Destination.Port),
		}
		copy(addr4.Source[:], p.Source.IP.To4())
		copy(addr4.Destination[:], p.Destination.IP.To4())
		addr = addr4
	case TCP6:
		header.Protocol = ProtocolTCP6
		addr6 := proxyV2Address6{
			SourcePort:      uint16(p.Source.Port),
			DestinationPort: uint16(p.Destination.Port),
		}
		copy(addr6.Source[:], p.Source.IP.To16())
		copy(addr6.Destination[:], p.Destination.IP.To16())
		addr = addr6
	}
	header.Length = uint16(binary.Size(addr))
	binary.Write(b, binary.BigEndian, header)
	binary.Write(b, binary.BigEndian, addr)

	return b.Bytes()
}

// ReadProxyLine reads proxy line protocol from the reader
func ReadProxyLine(reader *bufio.Reader) (*ProxyLine, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, trace.Wrap(err)
	}
	if !strings.HasSuffix(line, proxyCRLF) {
		return nil, trace.BadParameter("expected CRLF in proxy protocol, got something else")
	}
	tokens := strings.Split(line[:len(line)-2], proxySep)
	ret := ProxyLine{}
	if len(tokens) < 6 {
		return nil, trace.BadParameter("malformed PROXY line protocol string")
	}
	switch tokens[1] {
	case TCP4:
		ret.Protocol = TCP4
	case TCP6:
		ret.Protocol = TCP6
	default:
		ret.Protocol = UNKNOWN
	}
	sourceIP, err := parseIP(ret.Protocol, tokens[2])
	if err != nil {
		return nil, trace.Wrap(err)
	}
	destIP, err := parseIP(ret.Protocol, tokens[3])
	if err != nil {
		return nil, trace.Wrap(err)
	}
	sourcePort, err := parsePortNumber(tokens[4])
	if err != nil {
		return nil, trace.Wrap(err)
	}
	destPort, err := parsePortNumber(tokens[5])
	if err != nil {
		return nil, err
	}
	ret.Source = net.TCPAddr{IP: sourceIP, Port: sourcePort}
	ret.Destination = net.TCPAddr{IP: destIP, Port: destPort}
	return &ret, nil
}

func parsePortNumber(portString string) (int, error) {
	port, err := strconv.Atoi(portString)
	if err != nil {
		return -1, trace.BadParameter("bad port %q: %v", port, err)
	}
	if port < 0 || port > 65535 {
		return -1, trace.BadParameter("port %q not in supported range [0...65535]", portString)
	}
	return port, nil
}

func parseIP(protocol string, addrString string) (net.IP, error) {
	addr := net.ParseIP(addrString)
	switch {
	case len(addr) == 0:
		return nil, trace.BadParameter("failed to parse address")
	case addr.To4() != nil && protocol != TCP4:
		return nil, trace.BadParameter("got IPV4 address %q for IPV6 proto %q", addr.String(), protocol)
	case addr.To4() == nil && protocol == TCP6:
		return nil, trace.BadParameter("got IPV6 address %v %q for IPV4 proto %q", len(addr), addr.String(), protocol)
	}
	return addr, nil
}

type proxyV2Header struct {
	Signature      [12]uint8
	VersionCommand uint8
	Protocol       uint8
	Length         uint16
}

type proxyV2Address4 struct {
	Source          [4]uint8
	Destination     [4]uint8
	SourcePort      uint16
	DestinationPort uint16
}

type proxyV2Address6 struct {
	Source          [16]uint8
	Destination     [16]uint8
	SourcePort      uint16
	DestinationPort uint16
}

const (
	Version2     = 2
	ProxyCommand = 1
	LocalCommand = 0
	ProtocolTCP4 = 0x11
	ProtocolTCP6 = 0x21
)

func ReadProxyLineV2(reader *bufio.Reader) (*ProxyLine, error) {
	var header proxyV2Header
	var ret ProxyLine
	if err := binary.Read(reader, binary.BigEndian, &header); err != nil {
		return nil, trace.Wrap(err)
	}
	if !bytes.Equal(header.Signature[:], proxyV2Prefix) {
		return nil, trace.BadParameter("unrecognized signature %s", hex.EncodeToString(header.Signature[:]))
	}
	cmd, ver := header.VersionCommand&0xF, header.VersionCommand>>4
	if ver != Version2 {
		return nil, trace.BadParameter("unsupported version %d", ver)
	}
	if cmd == LocalCommand {
		// LOCAL command, just skip address information and keep original addresses (no proxy line)
		if header.Length > 0 {
			_, err := io.CopyN(io.Discard, reader, int64(header.Length))
			return nil, trace.Wrap(err)
		}
		return nil, nil
	}
	if cmd != ProxyCommand {
		return nil, trace.BadParameter("unsupported command %d", cmd)
	}
	var size uint16
	switch header.Protocol {
	case ProtocolTCP4:
		var addr proxyV2Address4
		size = uint16(unsafe.Sizeof(addr))
		if err := binary.Read(reader, binary.BigEndian, &addr); err != nil {
			return nil, trace.Wrap(err)
		}
		ret.Protocol = TCP4
		ret.Source = net.TCPAddr{IP: addr.Source[:], Port: int(addr.SourcePort)}
		ret.Destination = net.TCPAddr{IP: addr.Destination[:], Port: int(addr.DestinationPort)}
	case ProtocolTCP6:
		var addr proxyV2Address6
		size = uint16(unsafe.Sizeof(addr))
		if err := binary.Read(reader, binary.BigEndian, &addr); err != nil {
			return nil, trace.Wrap(err)
		}
		ret.Protocol = TCP6
		ret.Source = net.TCPAddr{IP: addr.Source[:], Port: int(addr.SourcePort)}
		ret.Destination = net.TCPAddr{IP: addr.Destination[:], Port: int(addr.DestinationPort)}
	default:
		return nil, trace.BadParameter("unsupported protocol %x", header.Protocol)
	}

	if header.Length > size {
		if _, err := io.CopyN(io.Discard, reader, int64(header.Length-size)); err != nil {
			return nil, trace.Wrap(err)
		}
	}

	return &ret, nil
}
