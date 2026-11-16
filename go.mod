module ps5cast

go 1.27.4

require (
	github.com/miekg/dns v1.1.73
	github.com/sirupsen/logrus v1.10.2
	github.com/yutopp/go-rtmp v0.0.7
	golang.org/x/mod v0.43.0
)

require (
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/yutopp/go-amf0 v0.1.1 // indirect
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/sync v0.25.0 // indirect
	golang.org/x/sys v0.50.0 // indirect
)

// The fork answers "connect" with the client's transaction ID: the PS5 sends 0
// and ignores the answer of the upstream library, which always carries 1.
replace github.com/yutopp/go-rtmp => github.com/AlanSnt/go-rtmp v0.0.8-ps5cast.1
