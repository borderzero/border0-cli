package util

import (
	"encoding/json"

	"google.golang.org/protobuf/types/known/structpb"
)

func AsStruct(data *structpb.Struct, target interface{}) error {
	bytes, err := data.MarshalJSON()
	if err != nil {
		return err
	}

	if err = json.Unmarshal(bytes, target); err != nil {
		return err
	}

	return nil
}
