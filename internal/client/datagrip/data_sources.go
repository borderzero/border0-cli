package datagrip

import (
	"crypto/x509"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"fmt"
	"io/ioutil"
)

type Config struct {
	Type        string // mysql or postgres
	Name        string
	Host        string
	Port        int
	Database    string // database name
	CAPath      string // cert chain path
	SSLCertPath string // cert pem file path
	SSLKeyPath  string // key pem file path
}

func DataSourcesXML(c *Config) (string, error) {
	// convert the key pem file to PKCS#8 format
	pkcs8KeyPath, err := keyPEMToPKCS8(c.SSLKeyPath)
	if err != nil {
		return "", fmt.Errorf("unable to convert key PEM to PKCS8: %w", err)
	}

	var (
		jdbcDriver, jdbcURL string
		driverProps         []prop
		sslConf             ssl
	)
	switch c.Type {
	case "mysql":
		jdbcDriver = "com.mysql.cj.jdbc.Driver"
		jdbcURL = fmt.Sprintf("jdbc:mysql://%s:%d/%s", c.Host, c.Port, c.Database)
		sslConf = ssl{
			ClientCert: c.SSLCertPath,
			ClientKey:  pkcs8KeyPath,
			Enabled:    true,
			Mode:       "REQUIRE",
		}
	case "postgres":
		jdbcDriver = "org.postgresql.Driver"
		jdbcURL = fmt.Sprintf("jdbc:postgresql://%s:%d/%s", c.Host, c.Port, c.Database)
		driverProps = []prop{
			{
				Name:  "sslmode",
				Value: "verify-ca",
			},
		}
		sslConf = ssl{
			CACert:     c.CAPath,
			ClientCert: c.SSLCertPath,
			ClientKey:  pkcs8KeyPath,
			Enabled:    true,
		}
	default:
		return "", fmt.Errorf("unsupported database type: %s", c.Type)
	}

	doc := xmlDoc{
		Version: 4,
		Component: comp{
			Name:      "DataSourceManagerImpl",
			Format:    "xml",
			Multifile: true,
			DataSource: source{
				Name:             fmt.Sprintf("%s@%s", c.Database, c.Host),
				Synchronize:      true,
				JdbcDriver:       jdbcDriver,
				JdbcURL:          jdbcURL,
				WorkingDir:       "$ProjectFileDir$",
				AuthProvider:     "no-auth",
				DriverProperties: driverProps,
				SSLConfig:        sslConf,
			},
		},
	}

	output, err := xml.MarshalIndent(&doc, "", "    ")
	if err != nil {
		return "", fmt.Errorf("error generating MySQL Workbench connections.xml: %w", err)
	}

	return xml.Header + string(output), nil
}

func keyPEMToPKCS8(keyPath string) (string, error) {
	// Read the private key file
	keyPEMBytes, err := ioutil.ReadFile(keyPath)
	if err != nil {
		return "", fmt.Errorf("error reading PEM file: %w", err)
	}

	// Decode the PEM block
	block, _ := pem.Decode(keyPEMBytes)
	if block == nil || block.Type != "RSA PRIVATE KEY" {
		return "", errors.New("failed to decode PEM block containing private key.")
	}

	// parse the private key
	priv, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("unable to parse PKCS8 private key: %w", err)
	}

	// marshal the private key into PKCS#8 DER format
	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", fmt.Errorf("unable to marshal private key to PKCS8: %w", err)
	}

	// write the PKCS#8 key to a file
	pkcs8KeyPath := keyPath + ".pk8"
	if err := ioutil.WriteFile(pkcs8KeyPath, pkcs8Bytes, 0600); err != nil {
		return "", fmt.Errorf("unable to write private key: %w", err)
	}

	return pkcs8KeyPath, nil
}

// MySQL example:
// <?xml version="1.0" encoding="UTF-8"?>
// <project version="4">
//   <component name="DataSourceManagerImpl" format="xml" multifile-model="true">
//     <data-source name="mysql@dbtest-rollie-ma.border0.io">
//       <synchronize>true</synchronize>
//       <jdbc-driver>com.mysql.cj.jdbc.Driver</jdbc-driver>
//       <jdbc-url>jdbc:mysql://dbtest-rollie-ma.border0.io:28034/mysql</jdbc-url>
//       <working-dir>$ProjectFileDir$</working-dir>
//       <auth-provider>no-auth</auth-provider>
//       <ssl-config>
//         <client-cert>$PROJECT_DIR$/../../e54dfb7e-6e95-4689-a134-6c4e2c9bf39b.crt</client-cert>
//         <client-key>$PROJECT_DIR$/../../e54dfb7e-6e95-4689-a134-6c4e2c9bf39b.key.pk8</client-key>
//         <enabled>true</enabled>
//         <mode>REQUIRE</mode>
//       </ssl-config>
//     </data-source>
//   </component>
// </project>

// PostgreSQL example:
// <?xml version="1.0" encoding="UTF-8"?>
// <project version="4">
//   <component name="DataSourceManagerImpl" format="xml" multifile-model="true">
//     <data-source name="postgres@pgtest-rollie-ma.border0.io">
//       <synchronize>true</synchronize>
//       <jdbc-driver>org.postgresql.Driver</jdbc-driver>
//       <jdbc-url>jdbc:postgresql://pgtest-rollie-ma.border0.io:17042/postgres</jdbc-url>
//       <working-dir>$ProjectFileDir$</working-dir>
//       <auth-provider>no-auth</auth-provider>
//       <driver-properties>
//         <property name="sslmode" value="verify-ca" />
//       </driver-properties>
//       <ssl-config>
//         <ca-cert>$PROJECT_DIR$/../../pgtest-rollie-ma.border0.io.chain.crt</ca-cert>
//         <client-cert>$USER_HOME$/.border0/e54dfb7e-6e95-4689-a134-6c4e2c9bf39b.crt</client-cert>
//         <client-key>$USER_HOME$/.border0/e54dfb7e-6e95-4689-a134-6c4e2c9bf39b.key.pk8</client-key>
//         <enabled>true</enabled>
//       </ssl-config>
//     </data-source>
//   </component>
// </project>

type xmlDoc struct {
	XMLName   xml.Name `xml:"project"`
	Version   int      `xml:"version,attr"`
	Component comp
}

type comp struct {
	XMLName    xml.Name `xml:"component"`
	Name       string   `xml:"name,attr"`
	Format     string   `xml:"format,attr"`
	Multifile  bool     `xml:"multifile-model,attr"`
	DataSource source
}

type source struct {
	XMLName          xml.Name `xml:"data-source"`
	Name             string   `xml:"name,attr"`
	Synchronize      bool     `xml:"synchronize"`
	JdbcDriver       string   `xml:"jdbc-driver"`
	JdbcURL          string   `xml:"jdbc-url"`
	WorkingDir       string   `xml:"working-dir"`
	AuthProvider     string   `xml:"auth-provider"`
	DriverProperties []prop   `xml:"driver-properties>property,omitempty""`
	SSLConfig        ssl
}

type prop struct {
	XMLName xml.Name `xml:"property"`
	Name    string   `xml:"name,attr"`
	Value   string   `xml:"value,attr"`
}

type ssl struct {
	XMLName    xml.Name `xml:"ssl-config"`
	CACert     string   `xml:"ca-cert,omitempty"`
	ClientCert string   `xml:"client-cert"`
	ClientKey  string   `xml:"client-key"`
	Enabled    bool     `xml:"enabled"`
	Mode       string   `xml:"mode,omitempty"`
}
