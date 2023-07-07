package util

import (
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"
)

// AsStruct unmarshals a structbp onto a given target object.
func AsStruct(structpb *structpb.Struct, target any) error {
	jsonBytes, err := structpb.MarshalJSON()
	if err != nil {
		return fmt.Errorf("failed to unmarshal structpb: %w", err)
	}
	if err = json.Unmarshal(jsonBytes, target); err != nil {
		return fmt.Errorf("failed to unmarshal json: %v", err)
	}
	return nil
}
