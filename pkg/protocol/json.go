package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/maomeng/aim/pkg/sequence"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Marshal implements AIM's public protobuf JSON contract. Only fields explicitly
// marked safe_sequence become numbers; timestamps and counts keep their own
// protobuf representation. Absent optional references remain absent.
func Marshal(message proto.Message) ([]byte, error) {
	raw, err := (protojson.MarshalOptions{UseProtoNames: true, UseEnumNumbers: true, EmitDefaultValues: true}).Marshal(message)
	if err != nil {
		return nil, err
	}
	return transform(raw, message.ProtoReflect().Descriptor(), true)
}

// Unmarshal rejects numeric entity IDs, quoted positions, and pseudo-identities.
// Missing fields stay missing; the owning boundary checks required references
// after filling authenticated identity and path parameters.
func Unmarshal(raw []byte, message proto.Message) error {
	// Let protobuf check unknown fields, duplicate names, oneofs and value types
	// before walking the original JSON (not a re-encoded, lossy generic map).
	if err := protojson.Unmarshal(raw, message); err != nil {
		return err
	}
	_, err := transform(raw, message.ProtoReflect().Descriptor(), false)
	return err
}

func marked(field protoreflect.FieldDescriptor, extension protoreflect.ExtensionType) bool {
	return proto.GetExtension(field.Options(), extension) == true
}

func transform(raw []byte, descriptor protoreflect.MessageDescriptor, output bool) ([]byte, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, fmt.Errorf("%s must be a JSON object", descriptor.FullName())
	}
	fields := descriptor.Fields()
	for name, value := range object {
		field := fields.ByName(protoreflect.Name(name))
		if field == nil {
			field = fields.ByJSONName(name)
		}
		if field == nil {
			continue // protojson has already checked unknown fields.
		}
		converted, err := transformField(value, field, output)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field.FullName(), err)
		}
		object[name] = converted
	}
	if err := validateReferences(object, fields); err != nil {
		return nil, fmt.Errorf("%s: %w", descriptor.FullName(), err)
	}
	if !output {
		return raw, nil
	}
	return json.Marshal(object)
}

func transformField(raw []byte, field protoreflect.FieldDescriptor, output bool) ([]byte, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if marked(field, common.E_SafeSequence) || ((marked(field, common.E_EntityId) || marked(field, common.E_SubmissionKey)) && !field.HasPresence()) {
			return nil, fmt.Errorf("required identity or sequence cannot be null")
		}
		return raw, nil
	}
	if field.IsMap() {
		var values map[string]json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, err
		}
		for key, value := range values {
			if marked(field, common.E_EntityId) {
				if err := identity.Validate(key); err != nil {
					return nil, err
				}
			}
			converted, err := transformScalar(value, field.MapValue(), output)
			if err != nil {
				return nil, err
			}
			values[key] = converted
		}
		if output {
			return json.Marshal(values)
		}
		return raw, nil
	}
	if field.IsList() {
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, err
		}
		for i, value := range values {
			converted, err := transformScalar(value, field, output)
			if err != nil {
				return nil, err
			}
			values[i] = converted
		}
		if output {
			return json.Marshal(values)
		}
		return raw, nil
	}
	return transformScalar(raw, field, output)
}

func transformScalar(raw []byte, field protoreflect.FieldDescriptor, output bool) ([]byte, error) {
	if marked(field, common.E_SafeSequence) {
		if output {
			var number string
			if err := json.Unmarshal(raw, &number); err != nil {
				return nil, err
			}
			value, err := strconv.ParseInt(number, 10, 64)
			if err != nil {
				return nil, err
			}
			if err := sequence.Validate(value); err != nil {
				return nil, err
			}
			return []byte(number), nil
		}
		_, err := sequence.ParseJSON(raw)
		return raw, err
	}
	if marked(field, common.E_EntityId) || marked(field, common.E_SubmissionKey) {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		if marked(field, common.E_SubmissionKey) {
			return raw, identity.ValidateSubmissionKey(value)
		}
		return raw, identity.Validate(value)
	}
	if field.Kind() == protoreflect.MessageKind && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return transform(raw, field.Message(), output)
	}
	return raw, nil
}

// Ownership and clearing are explicit field contracts, not ID-name heuristics.
func validateReferences(object map[string]json.RawMessage, fields protoreflect.FieldDescriptors) error {
	get := func(field protoreflect.FieldDescriptor) json.RawMessage {
		if raw, ok := object[string(field.Name())]; ok {
			return raw
		}
		return object[field.JSONName()]
	}
	present := func(raw []byte) bool { return len(raw) != 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }
	if ownerType := fields.ByName("owner_type"); ownerType != nil && (!ownerType.HasPresence() || present(get(ownerType))) {
		var kind string
		if err := json.Unmarshal(get(ownerType), &kind); err != nil {
			return err
		}
		owner := fields.ByName("owner_id")
		if owner == nil || (kind != "platform" && kind != "user") || (kind == "user") != present(get(owner)) {
			return fmt.Errorf("user ownership requires owner_id; platform ownership forbids it")
		}
	}
	for i := range fields.Len() {
		field := fields.Get(i)
		if !marked(field, common.E_EntityId) || !field.HasPresence() {
			continue
		}
		clearField := fields.ByName(protoreflect.Name("clear_" + string(field.Name())))
		if clearField == nil || !present(get(clearField)) {
			continue
		}
		var clear bool
		if err := json.Unmarshal(get(clearField), &clear); err != nil {
			return err
		}
		if clear && present(get(field)) {
			return fmt.Errorf("%s cannot be set and cleared together", field.Name())
		}
	}
	return nil
}
