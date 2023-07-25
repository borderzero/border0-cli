package util

import (
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"
)

// AsStruct converts a protobuf struct to a go struct
func AsStruct(structpb *structpb.Struct, gostruct any) error {
	jsonBytes, err := structpb.MarshalJSON()
	if err != nil {
		return fmt.Errorf("failed to marshal pb struct to json: %v", err)
	}
	if err = json.Unmarshal(jsonBytes, gostruct); err != nil {
		return fmt.Errorf("failed to unmarshal json onto go struct: %v", err)
	}
	return nil
}

// AsPbStruct converts a go struct to a protobuf struct
func AsPbStruct(gostruct any, structpb *structpb.Struct) error {
	jsonBytes, err := json.Marshal(gostruct)
	if err != nil {
		return fmt.Errorf("failed to marshal go struct to json: %v", err)
	}
	if err = json.Unmarshal(jsonBytes, &structpb); err != nil {
		return fmt.Errorf("failed to unmarshal json onto pb struct: %v", err)
	}
	return nil
}
