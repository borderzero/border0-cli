package discover

import (
	"strings"

	"github.com/mitchellh/mapstructure"
)

type SocketDataTag struct {
	Port             string `mapstructure:"port"`
	Type             string `mapstructure:"type"`
	Group            string `mapstructure:"group"`
	Host             string `mapstructure:"host"`
	Name             string `mapstructure:"name"`
	UpstreamUsername string `mapstructure:"upstream_username"`
	UpstreamPassword string `mapstructure:"upstream_password"`
	UpstreamType     string `mapstructure:"upstream_type"`
}

// Parse the tag and transform it into a structured data called SocketDataTag
// examples of tags:
// border0_ssh="port=22,type=ssh,group=allowed_users"
// border0_81="type=http,port=81,group=docker_team,name=ngx-srv1-p81"
// border0_01="type=database,port=3306,group=docker_team,upstream_type=mysql,upstream_user=root,upstream_pass=my-secret-pw,name=my-docker-mysql-db"
// NOTE: be aware of single and double quoting across different platforms, docker compose for example:
// labels:
// - "border0_80=type=http,port=80,group=my_super_ops_team"
// - "border0_81=type=http,port=81,group=my_super_ops_team,name=ngx-srv0-p81"

func parseLabels(tag string) SocketDataTag {
	return parseLabelsWithDelimeter(tag, ",")
}

func parseLabelsWithDelimeter(tag string, delimeter string) SocketDataTag {
	labels := map[string]string{}
	for _, label := range strings.Split(tag, delimeter) {
		label = strings.TrimSpace(label)
		if strings.Contains(label, "=") {
			kv := strings.Split(label, "=")
			if len(kv) >= 2 {
				labels[kv[0]] = kv[1]
			}
		}
	}

	data := SocketDataTag{}
	config := &mapstructure.DecoderConfig{
		WeaklyTypedInput: true,
		Result:           &data,
	}

	decoder, err := mapstructure.NewDecoder(config)
	if err != nil {
		return data
	}

	decoder.Decode(labels)
	return data
}
