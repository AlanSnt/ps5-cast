module ps5cast

go 1.27.4

require (
	github.com/miekg/dns v1.1.73
	github.com/shirou/gopsutil/v4 v4.26.10
	github.com/sirupsen/logrus v1.10.2
	github.com/yusufpapurcu/wmi v1.2.4
	github.com/yutopp/go-rtmp v0.0.7
	golang.org/x/mod v0.43.0
)

require (
	github.com/ebitengine/purego v0.11.2 // indirect
	github.com/go-ole/go-ole v1.2.6 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/lufia/plan9stats v0.0.0-20211012122336-39d0f177ccd0 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/power-devops/perfstat v0.0.0-20260805114148-88456608a4f6 // indirect
	github.com/tklauser/go-sysconf v0.4.0 // indirect
	github.com/tklauser/numcpus v0.12.0 // indirect
	github.com/yutopp/go-amf0 v0.1.1 // indirect
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/sync v0.25.0 // indirect
	golang.org/x/sys v0.50.0 // indirect
)

// The fork answers "connect" with the client's transaction ID: the PS5 sends 0
// and ignores the answer of the upstream library, which always carries 1.
// It also reads an empty message instead of panicking, which cut the stream.
replace github.com/yutopp/go-rtmp => github.com/AlanSnt/go-rtmp v0.0.8-ps5cast.2
