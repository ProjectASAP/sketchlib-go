package testdata

import (
	"compress/gzip"
	"fmt"
	"log"
	"net"
	"os"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcapgo"
)

const maxPackets = 2000000

// Sample represents a streaming sample for sketch evaluation
type Sample struct {
	T int64   // sequential timestamp (packet index)
	F float64 // numeric source IP
}

// IPv4ToUint32 converts IPv4 to uint32
func IPv4ToUint32(ip net.IP) uint32 {
	ip = ip.To4()
	return uint32(ip[0])<<24 |
		uint32(ip[1])<<16 |
		uint32(ip[2])<<8 |
		uint32(ip[3])
}

// ReadCAIDAStream reads two CAIDA pcap.gz files and returns stream samples
func ReadCAIDAStream(file1, file2 string) ([]Sample, error) {
	files := []string{file1, file2}
	stream := make([]Sample, 0, maxPackets)

	var t int64 = 0

	for _, fname := range files {

		f, err := os.Open(fname)
		if err != nil {
			return nil, err
		}

		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}

		reader, err := pcapgo.NewReader(gz)
		if err != nil {
			return nil, err
		}

		packetSource := gopacket.NewPacketSource(reader, reader.LinkType())

		for packet := range packetSource.Packets() {

			ipLayer := packet.Layer(layers.LayerTypeIPv4)
			if ipLayer == nil {
				continue
			}

			ip := ipLayer.(*layers.IPv4)
			srcUint := IPv4ToUint32(ip.SrcIP)

			t++
			stream = append(stream, Sample{
				T: t,
				F: float64(srcUint),
			})

			if t >= maxPackets {
				gz.Close()
				f.Close()
				fmt.Println("Total packets processed:", t)
				return stream, nil
			}
		}

		gz.Close()
		f.Close()
	}

	fmt.Println("Total packets processed:", t)
	return stream, nil
}

func main() {

	file1 := "caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"
	file2 := "caida/equinix-nyc.dirA.20181220-130300.UTC.anon.pcap.gz"

	stream, err := ReadCAIDAStream(file1, file2)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Final stream size:", len(stream))
}
