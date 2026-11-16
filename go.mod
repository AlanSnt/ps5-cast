module ps5cast

go 1.24

replace (
	golang.org/x/crypto => github.com/golang/crypto v0.32.0
	golang.org/x/mod => github.com/golang/mod v0.22.0
	golang.org/x/net => github.com/golang/net v0.34.0
	golang.org/x/sync => github.com/golang/sync v0.10.0
	golang.org/x/sys => github.com/golang/sys v0.30.0
	golang.org/x/text => github.com/golang/text v0.21.0
	golang.org/x/tools => github.com/golang/tools v0.29.0
)

replace gopkg.in/yaml.v3 => github.com/go-yaml/yaml/v3 v3.0.1

replace gopkg.in/check.v1 => github.com/go-check/check v0.0.0-20201130134442-10cb98267c6c

require (
	github.com/miekg/dns v1.1.62
	github.com/sirupsen/logrus v1.7.0
	github.com/yutopp/go-rtmp v0.0.7
)

require (
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.0 // indirect
	github.com/mitchellh/mapstructure v1.4.1 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/yutopp/go-amf0 v0.1.0 // indirect
	golang.org/x/mod v0.22.0 // indirect
	golang.org/x/net v0.34.0 // indirect
	golang.org/x/sync v0.10.0 // indirect
	golang.org/x/sys v0.29.0 // indirect
	golang.org/x/tools v0.22.0 // indirect
)
