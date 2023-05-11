#!/bin/bash
export DatabasePassword=${DatabasePassword:?"Please specify a DatabasePassword variable."}

mysql -h {{.DatabaseHostname}} -u {{.DatabaseAdminUsername}} -P 3306 -e "CREATE DATABASE IF NOT EXISTS {{.DatabaseName}}; GRANT ALL PRIVILEGES ON {{.DatabaseName}}.* TO '{{.DatabaseName}}'@'localhost' IDENTIFIED BY '$DatabasePassword';GRANT ALL PRIVILEGES ON {{.DatabaseName}}.* TO '{{.DatabaseName}}'@'%' IDENTIFIED BY '$DatabasePassword'; CREATE USER IF NOT EXISTS '{{.DatabaseName}}_tls' REQUIRE SUBJECT '/O=openstack/OU=mariadb/CN={{.DatabaseName}}'; GRANT ALL PRIVILEGES ON {{.DatabaseName}}.* TO '{{.DatabaseName}}_tls'@'%';"
