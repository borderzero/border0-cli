package varsource

import "context"

// VariableSource represents the necessary functionality of a source of variables.
type VariableSource interface {
	// GetVariables takes a map of variable-name to variable-definition
	// and returns a map of variable-name to variable-value. i.e. returns
	// the same map except that the variable definitions are replaced with
	// the actual variable values.
	//
	// For example:
	// {
	//   "DB_USERNAME": "from:env:DB_USERNAME",
	//   "DB_PASSWORD": "from:file:~/.creds/password.txt",
	// }
	// would be translated to:
	// {
	//   "DB_USERNAME": "database-user-xyz",
	//   "DB_PASSWORD": "df29^%qd3gs8&*&(asd8t\tqe=",
	// }
	GetVariables(ctx context.Context, vars map[string]string) (map[string]string, error)
	GetVariable(ctx context.Context, varDefn string) (string, error)
}

// NewDefaultVariableSource returns the default VariableSource implementation.
func NewDefaultVariableSource() VariableSource {
	return NewMultipleUpstreamVariableSource(
		WithTopLevelPrefix(defaultTopLevelPrefix),
		WithEnvVariableUpstream(),
		WithFileVariableUpstream(),
		WithShellVariableUpstream(),
		WithAWSSSMVariableUpstream(),
		WithAWSSecretsManagerVariableUpstream(),
	)
}
