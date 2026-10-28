package main

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const sampleNetBoxCSV = `IP Address,VRF,Status,Role,Tenant,Assigned,DNS name,Description,ID,Tenant Group,NAT (Inside),NAT (Outside),Comments,Contacts,Tags,Created,Last updated,Owner Group,Owner,Interface,Parent,DNS Address Records
10.70.70.35/23,Global,Active,,,,,IP discovered by network discovery,552,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-10T06:30:13.979469+00:00,2026-02-10T06:30:13.979483+00:00,,,,,
10.70.70.36/23,Global,Active,,,,,IP discovered by network discovery,553,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-10T06:30:14.126553+00:00,2026-02-10T06:30:14.126564+00:00,,,,,
10.70.70.64/23,Global,Active,,,,,IP discovered by network discovery,554,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-12T07:00:07.797651+00:00,2026-02-12T07:00:07.797673+00:00,,,,,
10.70.70.78/23,Global,Active,,,,,IP discovered by network discovery,556,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-12T14:30:09.923944+00:00,2026-02-12T14:30:09.923960+00:00,,,,,
10.70.70.80/23,Global,Active,,,,,IP discovered by network discovery,555,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-12T10:30:08.933854+00:00,2026-02-12T10:30:08.933866+00:00,,,,,
10.70.70.87/23,Global,Active,,,,,IP discovered by network discovery,557,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-13T03:30:08.547988+00:00,2026-02-13T03:30:08.548020+00:00,,,,,
10.70.70.88/23,Global,Active,,,,,IP discovered by network discovery,558,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-13T03:30:08.689156+00:00,2026-02-13T03:30:08.689178+00:00,,,,,
10.70.70.89/23,Global,Active,,,,,IP discovered by network discovery,559,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-13T03:30:08.925697+00:00,2026-02-13T03:30:08.925708+00:00,,,,,
10.70.70.90/23,Global,Active,,,,,IP discovered by network discovery,560,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-13T03:30:09.020645+00:00,2026-02-13T03:30:09.020655+00:00,,,,,
10.70.70.91/23,Global,Active,,,,,IP discovered by network discovery,561,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-13T04:30:09.537684+00:00,2026-02-13T04:30:09.537695+00:00,,,,,
10.70.70.95/23,Global,Active,,,,,IP discovered by network discovery,562,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-13T08:00:08.583429+00:00,2026-02-13T08:00:08.583444+00:00,,,,,
10.70.70.101/23,Global,Active,,,,,IP discovered by network discovery,563,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-13T08:00:08.884153+00:00,2026-02-13T08:00:08.884171+00:00,,,,,
10.70.70.102/23,Global,Active,,,,,IP discovered by network discovery,564,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-13T08:00:09.043974+00:00,2026-02-13T08:00:09.043993+00:00,,,,,
10.70.70.105/23,Global,Active,,,,,IP discovered by network discovery,565,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-13T10:30:07.621753+00:00,2026-02-13T10:30:07.621766+00:00,,,,,
10.70.70.113/23,Global,Active,,,,,IP discovered by network discovery,566,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-14T03:00:07.657915+00:00,2026-02-14T03:00:07.657925+00:00,,,,,
10.70.70.114/23,Global,Active,,,,,IP discovered by network discovery,567,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-14T03:00:07.806333+00:00,2026-02-14T03:00:07.806352+00:00,,,,,
10.70.70.119/23,Global,Active,,,,,IP discovered by network discovery,531,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-02T05:30:08.926827+00:00,2026-02-02T05:30:08.926844+00:00,,,,,
10.70.70.125/23,Global,Active,,,,,IP discovered by network discovery,568,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-15T04:30:05.508231+00:00,2026-02-15T04:30:05.508249+00:00,,,,,
10.70.70.131/23,Global,Active,,,,,IP discovered by network discovery,569,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-15T11:30:04.535380+00:00,2026-02-15T11:30:04.535394+00:00,,,,,
10.70.70.145/23,Global,Active,,,,,IP discovered by network discovery,570,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-16T04:30:09.343011+00:00,2026-02-16T04:30:09.343021+00:00,,,,,
10.70.70.147/23,Global,Active,,,,,IP discovered by network discovery,571,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-16T08:30:07.418234+00:00,2026-02-16T08:30:07.418249+00:00,,,,,
10.70.70.149/23,Global,Active,,,,,IP discovered by network discovery,572,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-16T08:30:07.714876+00:00,2026-02-16T08:30:07.714886+00:00,,,,,
10.70.70.158/23,Global,Active,,,,,IP discovered by network discovery,573,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-16T08:30:07.869412+00:00,2026-02-16T08:30:07.869427+00:00,,,,,
10.70.70.187/23,Global,Active,,,,,IP discovered by network discovery,574,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-18T18:00:04.446016+00:00,2026-02-18T18:00:04.446038+00:00,,,,,
10.70.70.193/23,Global,Active,,,,,IP discovered by network discovery,575,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-19T03:30:07.982155+00:00,2026-02-19T03:30:07.982170+00:00,,,,,
10.70.70.233/23,Global,Active,,,,,IP discovered by network discovery,532,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-02T05:30:10.983338+00:00,2026-02-02T05:30:10.983352+00:00,,,,,
10.70.70.241/23,Global,Active,,,,,IP discovered by network discovery,533,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-02T05:30:11.381850+00:00,2026-02-02T05:30:11.381860+00:00,,,,,
10.70.71.23/23,Global,Active,,,,,IP discovered by network discovery,534,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-02T05:30:11.832974+00:00,2026-02-02T05:30:11.832982+00:00,,,,,
10.70.71.24/23,Global,Active,,,,,IP discovered by network discovery,535,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-02T05:30:12.030975+00:00,2026-02-02T05:30:12.030982+00:00,,,,,
10.70.71.40/23,Global,Active,,,,,IP discovered by network discovery,538,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-03T22:00:16.873796+00:00,2026-02-03T22:00:16.873810+00:00,,,,,
10.70.71.60/23,Global,Active,,,,,IP discovered by network discovery,540,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-04T01:00:06.382322+00:00,2026-02-04T01:00:06.382333+00:00,,,,,
10.70.71.64/23,Global,Active,,,,,IP discovered by network discovery,541,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-04T01:00:06.583229+00:00,2026-02-04T01:00:06.583245+00:00,,,,,
10.70.71.66/23,Global,Active,,,,,IP discovered by network discovery,542,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-04T01:30:09.955016+00:00,2026-02-04T01:30:09.955029+00:00,,,,,
10.70.71.73/23,Global,Active,,,,,IP discovered by network discovery,543,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-04T04:00:09.639999+00:00,2026-02-04T04:00:09.640017+00:00,,,,,
10.70.71.82/23,Global,Active,,,,,IP discovered by network discovery,544,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-04T07:30:12.721002+00:00,2026-02-04T07:30:12.721020+00:00,,,,,
10.70.71.89/23,Global,Active,,,,,IP discovered by network discovery,545,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-04T18:00:05.295981+00:00,2026-02-04T18:00:05.296002+00:00,,,,,
10.70.71.91/23,Global,Active,,,,,IP discovered by network discovery,547,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-05T05:30:09.576588+00:00,2026-02-05T05:30:09.576599+00:00,,,,,
10.70.71.92/23,Global,Active,,,,,IP discovered by network discovery,546,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-05T02:30:08.009420+00:00,2026-02-05T02:30:08.009436+00:00,,,,,
10.70.71.98/23,Global,Active,,,,,IP discovered by network discovery,548,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-05T09:00:09.708646+00:00,2026-02-05T09:00:09.708666+00:00,,,,,
10.70.71.99/23,Global,Active,,,,,IP discovered by network discovery,549,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-05T09:00:09.856033+00:00,2026-02-05T09:00:09.856043+00:00,,,,,
10.70.71.221/23,Global,Active,,,145,pve01-thinkcentre01,IP discovered by network discovery,47,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2025-09-17T15:45:31.600843+00:00,2025-10-15T07:12:56.672137+00:00,,,vmbr1,PVE01,
10.70.71.222/23,Global,Active,,,150,pve02-thinkcentre02,IP discovered by network discovery,48,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2025-09-17T15:45:31.676481+00:00,2025-10-15T07:14:55.823603+00:00,,,vmbr1,PVE02,
10.70.71.223/23,Global,Active,,,156,pve03-thinkcentre03,IP discovered by network discovery,49,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2025-09-17T15:45:31.754300+00:00,2025-10-15T07:15:24.718414+00:00,,,vmbr1,PVE03,
10.70.71.224/23,Global,Active,,,160,pve04-macpro,IP discovered by network discovery,50,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2025-09-17T15:45:31.835336+00:00,2025-10-15T07:15:35.751904+00:00,,,vmbr1,PVE04,
10.70.71.231/23,Global,Active,,,216,pa-440,IP discovered by network discovery,51,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2025-09-17T15:45:31.911973+00:00,2025-11-15T05:10:56.898698+00:00,,,MGMT,PA-440,
10.70.71.232/24,Global,Active,,,241,,,579,,,,,,,2026-02-20T16:52:48.693941+00:00,2026-02-20T16:52:48.693951+00:00,,,eth0,AKLAB-AP01,
10.70.71.251/23,Global,Active,,,,,IP discovered by network discovery,550,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-08T11:00:08.605406+00:00,2026-02-08T11:00:08.605425+00:00,,,,,
10.70.71.253/23,Global,Active,,,63,jump-vyos,IP discovered by network discovery,60,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2025-09-20T15:19:36.193446+00:00,2025-09-22T05:51:32.426266+00:00,,,Ethernet2.770,jump-vyos,
10.70.71.254/23,Global,Active,,,12,_gateway,IP discovered by network discovery,252,,,133.101.25.85/23,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2025-10-10T11:30:09.371794+00:00,2025-10-15T07:24:35.832517+00:00,,,eth16,lab-vyos,
10.70.72.51/24,Global,Active,,,161,arkime,IP discovered by network discovery,4,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2025-09-17T15:45:26.706637+00:00,2025-10-15T07:34:36.133416+00:00,,,enp3s0,IdeaCentre,
10.70.72.52/24,Global,Active,,,146,pve01,IP discovered by network discovery,5,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2025-09-17T15:45:26.855758+00:00,2025-10-15T07:16:04.930729+00:00,,,vmbr0,PVE01,
10.70.72.56/24,Global,Active,,,151,pve02,IP discovered by network discovery,8,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2025-09-17T15:45:27.199630+00:00,2025-10-15T07:16:17.959734+00:00,,,vmbr0,PVE02,
10.70.72.58/24,Global,Active,,,155,pve04,IP discovered by network discovery,10,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2025-09-17T15:45:27.401188+00:00,2025-10-15T07:16:39.028152+00:00,,,vmbr0,PVE03,
10.70.72.59/24,Global,Active,,,159,pve03,IP discovered by network discovery,11,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2025-09-17T15:45:27.501578+00:00,2025-10-15T07:16:51.189564+00:00,,,vmbr0,PVE04,
10.70.72.78/24,Global,Active,,,,,IP discovered by network discovery,536,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-02T05:30:14.683100+00:00,2026-02-02T05:30:14.683110+00:00,,,,,
10.70.72.79/24,Global,Active,,,,,IP discovered by network discovery,551,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-08T11:00:10.005438+00:00,2026-02-08T11:00:10.005457+00:00,,,,,
10.70.72.239/24,Global,Active,,,238,,,576,,,,,,,2026-02-20T16:02:08.368628+00:00,2026-02-20T16:02:08.368638+00:00,,,vme.0,ex2300edgeSW07,
10.70.72.246/24,Global,Active,,,244,,IP discovered by network discovery,539,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2026-02-03T22:00:21.382242+00:00,2026-03-23T15:17:14.864721+00:00,,,gi10,c1200-8p-edgeSW05,
10.70.72.247/24,Global,Active,,,219,,IP discovered by network discovery,537,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,"device-discovery,network_discovery",2026-02-02T05:30:15.033169+00:00,2026-02-04T00:00:05.282278+00:00,,,Vlan772,c9200edgeSW04,
10.70.72.248/24,Global,Active,,,189,edgesw03,IP discovered by network discovery,528,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,"device-discovery,network_discovery",2025-11-13T16:00:30.250034+00:00,2025-11-14T16:16:38.196317+00:00,,,Vlan772,c9200edgeSW03,
10.70.72.249/24,Global,Active,,,3,edgesw02,IP discovered by network discovery,1,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,"device-discovery,network_discovery",2025-09-17T15:33:53.121866+00:00,2025-09-22T05:56:16.920686+00:00,,,Vlan772,c1000edgeSW02,
10.70.72.250/24,Global,Active,,,5,edgesw01,IP discovered by network discovery,2,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,"device-discovery,network_discovery",2025-09-17T15:33:53.609133+00:00,2025-09-22T05:56:25.480337+00:00,,,Vlan772,c1000edgeSW01,
10.70.72.251/24,Global,Active,,,42,c9200,IP discovered by network discovery,61,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,"device-discovery,network_discovery",2025-09-20T15:20:37.932582+00:00,2025-09-23T00:00:06.334180+00:00,,,Vlan772,c1200coreSW,
10.70.72.252/24,Global,Active,,,170,thinla,IP discovered by network discovery,83,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2025-09-22T05:00:27.882661+00:00,2025-10-15T07:28:04.385025+00:00,,,eno1.772,aklabgw,
10.70.72.253/24,Global,Active,,,5,lab-vyos,IP discovered by network discovery,253,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2025-10-10T11:30:11.123784+00:00,2025-10-15T07:28:39.836411+00:00,,,eth3,lab-vyos,
10.70.72.254/24,Global,Active,,,62,jump-vyos,IP discovered by network discovery,71,,,,"{""hostnames"":null,""ports"":null,""extra_ports"":null}",,network_discovery,2025-09-20T16:24:24.749858+00:00,2025-09-22T05:57:44.305257+00:00,,,Ethernet1.772,jump-vyos,
10.70.77.247/24,Global,Active,,,224,,,578,,,,,,,2026-02-20T16:22:05.138573+00:00,2026-02-20T16:22:05.138582+00:00,,,Vlan777,c1000edgeSW02,
10.70.77.251/24,Global,Active,,,239,,,577,,,,,,,2026-02-20T16:20:06.916853+00:00,2026-02-20T16:20:06.916863+00:00,,,Vlan777,c1200coreSW,
10.70.96.1/24,Global,Active,,,243,,,580,,,,,,,2026-02-20T17:58:36.236476+00:00,2026-02-20T17:58:36.236486+00:00,,,Vlan960,c1200coreSW,
133.101.25.85/23,Global,Active,,,4,aklab.kyoto-su.ac.jp,lab-VyOSが持つ京都産業大学のグローバルIPアドレス,256,,10.70.71.254/23,,,,,2025-10-15T07:26:39.049436+00:00,2025-10-15T07:26:39.049446+00:00,,,eth1,lab-vyos,
192.168.91.251/24,Global,Active,,,43,,,58,,,,,,device-discovery,2025-09-17T15:47:39.608653+00:00,2025-09-17T15:47:39.608666+00:00,,,Vlan991,c1200coreSW,
192.168.92.251/24,Global,Active,,,44,,,59,,,,,,device-discovery,2025-09-17T15:47:40.290983+00:00,2025-09-17T15:47:40.290997+00:00,,,Vlan992,c1200coreSW,
`

func TestNetBoxCSV_SampleExport(t *testing.T) {
	dir := t.TempDir()
	path := writeTestFile(t, dir, "netbox.csv", sampleNetBoxCSV)

	doc, err := parseHostsFile(path)
	if err != nil {
		t.Fatalf("parseHostsFile: %v", err)
	}
	if err := validateHostsDoc(doc); err != nil {
		t.Fatalf("validateHostsDoc: %v", err)
	}
	if len(doc.Hosts) != 72 {
		t.Fatalf("len(doc.Hosts) = %d, want 72", len(doc.Hosts))
	}

	byHost := make(map[string]string, len(doc.Hosts))
	for _, h := range doc.Hosts {
		byHost[h.Host] = h.Name
	}

	wantNames := map[string]string{
		"10.70.70.35":    "",
		"10.70.71.221":   "pve01-thinkcentre01",
		"10.70.71.231":   "pa-440",
		"10.70.71.232":   "AKLAB-AP01",
		"10.70.71.253":   "jump-vyos (Ethernet2.770)",
		"10.70.71.254":   "_gateway",
		"10.70.72.51":    "arkime",
		"10.70.72.239":   "ex2300edgeSW07",
		"10.70.72.246":   "c1200-8p-edgeSW05",
		"10.70.72.247":   "c9200edgeSW04",
		"10.70.72.248":   "edgesw03",
		"10.70.72.251":   "c9200",
		"10.70.72.253":   "lab-vyos",
		"10.70.72.254":   "jump-vyos (Ethernet1.772)",
		"10.70.77.247":   "c1000edgeSW02",
		"10.70.77.251":   "c1200coreSW (Vlan777)",
		"10.70.96.1":     "c1200coreSW (Vlan960)",
		"133.101.25.85":  "aklab.kyoto-su.ac.jp",
		"192.168.91.251": "c1200coreSW (Vlan991)",
		"192.168.92.251": "c1200coreSW (Vlan992)",
	}
	for host, wantName := range wantNames {
		gotName, ok := byHost[host]
		if !ok {
			t.Errorf("host %q not found in parsed output", host)
			continue
		}
		if gotName != wantName {
			t.Errorf("host %q: Name = %q, want %q", host, gotName, wantName)
		}
	}
}

func TestNetBoxCSV_IncludeInYAML(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "netbox.csv", sampleNetBoxCSV)
	yamlPath := writeTestFile(t, dir, "hosts.yaml", "groups:\n  - name: Lab\n    include: netbox.csv\n")

	doc, err := parseHostsFile(yamlPath)
	if err != nil {
		t.Fatalf("parseHostsFile: %v", err)
	}
	if err := validateHostsDoc(doc); err != nil {
		t.Fatalf("validateHostsDoc: %v", err)
	}
	if len(doc.Groups) != 1 || len(doc.Groups[0].Hosts) != 72 {
		t.Fatalf("groups = %+v, want 1 group with 72 hosts", doc.Groups)
	}
}

func TestNetBoxCSV_IncludeTildePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeTestFile(t, home, "netbox.csv", sampleNetBoxCSV)
	yamlPath := writeTestFile(t, t.TempDir(), "hosts.yaml", "include: ~/netbox.csv\n")

	doc, err := parseHostsFile(yamlPath)
	if err != nil {
		t.Fatalf("parseHostsFile: %v", err)
	}
	if len(doc.Hosts) != 72 {
		t.Fatalf("len(doc.Hosts) = %d, want 72", len(doc.Hosts))
	}
}

func TestNetBoxCSV_CheckConfigCLI(t *testing.T) {
	dir := t.TempDir()
	csvPath := writeTestFile(t, dir, "netbox.csv", sampleNetBoxCSV)

	var out, errOut bytes.Buffer
	code := run([]string{"-f", csvPath, "--check-config"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("run exited %d, stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), `"valid": true`) || !strings.Contains(out.String(), `"jump-vyos (Ethernet2.770)"`) {
		t.Fatalf("unexpected check-config output:\n%s", out.String())
	}
}

func TestNetBoxCSV_StatusFilteringAndVariants(t *testing.T) {
	content := "\xef\xbb\xbf# NetBox export\n" +
		"Address,Status,DNS_Name,Device,Virtual Machine,Interface\n" +
		"10.0.0.1/24,Active,web01,srv01,,eth0\n" +
		"10.0.0.2/24,Deprecated,old01,srv02,,eth0\n" +
		"10.0.0.3/24,Reserved,res01,,,\n" +
		"10.0.0.4/24,DHCP,dhcp01,,,\n" +
		"10.0.0.5/24,active,,srv05,,eth0\n" +
		"2001:db8::1/64,,, ,vm01,ens3\n" +
		"10.0.0.6,,,,,\n"

	dir := t.TempDir()
	path := writeTestFile(t, dir, "export.txt", content)

	doc, err := parseHostsFile(path)
	if err != nil {
		t.Fatalf("parseHostsFile: %v", err)
	}
	want := []hostEntry{
		{Host: "10.0.0.1", Name: "web01"},
		{Host: "10.0.0.5", Name: "srv05"},
		{Host: "2001:db8::1", Name: "vm01"},
		{Host: "10.0.0.6"},
	}
	if !reflect.DeepEqual(doc.Hosts, want) {
		t.Fatalf("got %#v, want %#v", doc.Hosts, want)
	}
}

func TestNetBoxCSV_DisambiguationEdgeCases(t *testing.T) {
	content := "IP Address,Status,DNS name,Interface,Parent\n" +
		// Same baseName, no interface -> disambiguate with IP
		"10.0.0.1/24,Active,dup-no-iface,,\n" +
		"10.0.0.2/24,Active,dup-no-iface,,\n" +
		// Same baseName and same interface -> disambiguate with interface + IP
		"10.0.0.3/24,Active,dup-same-iface,eth0,\n" +
		"10.0.0.4/24,Active,dup-same-iface,eth0,\n" +
		// Identical row repeated -> disambiguate with #2
		"10.0.0.5/24,Active,exact-dup,eth0,\n" +
		"10.0.0.5/24,Active,exact-dup,eth0,\n"

	dir := t.TempDir()
	path := writeTestFile(t, dir, "edge.csv", content)

	doc, err := parseHostsFile(path)
	if err != nil {
		t.Fatalf("parseHostsFile: %v", err)
	}
	if err := validateHostsDoc(doc); err != nil {
		t.Fatalf("validateHostsDoc: %v", err)
	}
	want := []hostEntry{
		{Host: "10.0.0.1", Name: "dup-no-iface (10.0.0.1)"},
		{Host: "10.0.0.2", Name: "dup-no-iface (10.0.0.2)"},
		{Host: "10.0.0.3", Name: "dup-same-iface (eth0 10.0.0.3)"},
		{Host: "10.0.0.4", Name: "dup-same-iface (eth0 10.0.0.4)"},
		{Host: "10.0.0.5", Name: "exact-dup (eth0 10.0.0.5)"},
		{Host: "10.0.0.5", Name: "exact-dup (eth0 10.0.0.5) #2"},
	}
	if !reflect.DeepEqual(doc.Hosts, want) {
		t.Fatalf("got %#v, want %#v", doc.Hosts, want)
	}
}

func TestNetBoxCSV_Errors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name:    "empty IP address column",
			content: "IP Address,Status\n,Active\n",
			wantErr: "line 2: IP Address column is empty",
		},
		{
			name:    "invalid CIDR in IP address column",
			content: "IP Address,Status\nnot-an-ip/24,Active\n",
			wantErr: "line 2: invalid IP address",
		},
		{
			name:    "invalid bare IP in IP address column",
			content: "IP Address,Status\ns3cret-hostname,Active\n",
			wantErr: "line 2: invalid IP address",
		},
		{
			name:    "malformed CSV quote",
			content: "IP Address,Status,Comments\n10.0.0.1/24,Active,\"unclosed\n",
			wantErr: "line 2:",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTestFile(t, t.TempDir(), "bad.csv", tt.content)
			_, err := readIncludeFile(path)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), "s3cret") {
				t.Fatalf("error %q echoes file content", err)
			}
		})
	}
}

func TestNetBoxCSV_StopsAtTotalLimit(t *testing.T) {
	var b strings.Builder
	b.WriteString("IP Address,Status\n")
	for i := range maxHostsFileTargets + 2 {
		fmt.Fprintf(&b, "10.0.%d.%d/24,Active\n", i/256, i%256)
	}
	path := writeTestFile(t, t.TempDir(), "huge.csv", b.String())
	_, err := parseHostsFile(path)
	if err == nil || !strings.Contains(err.Error(), "more than 4096 targets") {
		t.Fatalf("err = %v, want total-limit error", err)
	}
}
