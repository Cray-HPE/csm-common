module github.com/Cray-HPE/csm-common/go

replace (
	k8s.io/client-go => k8s.io/client-go v0.22.2
	k8s.io/kubectl => k8s.io/kubectl v0.22.2
)

require (
	github.com/Cray-HPE/hms-base v1.15.2-0.20210928201115-8d9f61f26219
	github.com/Cray-HPE/hms-bss v1.9.5
	github.com/Cray-HPE/hms-shcd-parser v1.6.2
	github.com/Cray-HPE/hms-sls v1.13.0
	github.com/asaskevich/govalidator v0.0.0-20200907205600-7a23bdc65eef
	github.com/gocarina/gocsv v0.0.0-20200925213129-04be9ee2e1a2
	github.com/imdario/mergo v0.3.11 // indirect
	github.com/mitchellh/mapstructure v1.4.3
	github.com/pkg/errors v0.9.1
	github.com/smartystreets/assertions v1.0.0 // indirect
	github.com/spf13/cobra v1.1.3
	github.com/spf13/viper v1.7.1
	github.com/stretchr/testify v1.7.0
	go.etcd.io/etcd/api/v3 v3.5.1
	go.etcd.io/etcd/client/v3 v3.5.1
	go.uber.org/zap v1.17.0
	golang.org/x/net v0.7.0 // indirect
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.22.2
	k8s.io/apimachinery v0.22.2
	k8s.io/client-go v0.22.2
	k8s.io/kubectl v0.22.2
)

go 1.16
