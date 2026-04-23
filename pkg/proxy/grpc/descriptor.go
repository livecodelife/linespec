package grpc

import (
	"fmt"
	"os"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/dynamicpb"
)

type DescriptorResolver struct {
	files *protoregistry.Files
}

func LoadDescriptorSet(path string) (*DescriptorResolver, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read descriptor set: %w", err)
	}

	fds := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(data, fds); err != nil {
		return nil, fmt.Errorf("failed to unmarshal descriptor set: %w", err)
	}

	files, err := protodesc.NewFiles(fds)
	if err != nil {
		return nil, fmt.Errorf("failed to create files from descriptor set: %w", err)
	}

	return &DescriptorResolver{files: files}, nil
}

func (d *DescriptorResolver) JSONToProtobuf(service, method string, jsonData []byte) ([]byte, error) {
	if d == nil || d.files == nil {
		return nil, fmt.Errorf("no descriptor loaded")
	}

	desc, err := d.files.FindDescriptorByName(protoreflect.FullName(service))
	if err != nil {
		return nil, fmt.Errorf("service %s not found in descriptor: %w", service, err)
	}

	svcDesc, ok := desc.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("%s is not a service", service)
	}

	methodDesc := svcDesc.Methods().ByName(protoreflect.Name(method))
	if methodDesc == nil {
		return nil, fmt.Errorf("method %s not found in service %s", method, service)
	}

	outputDesc := methodDesc.Output()
	msg := dynamicpb.NewMessage(outputDesc)

	if err := protojson.Unmarshal(jsonData, msg); err != nil {
		return nil, fmt.Errorf("protojson unmarshal failed: %w", err)
	}

	return proto.Marshal(msg)
}

func (d *DescriptorResolver) HasDescriptor(service, method string) bool {
	if d == nil || d.files == nil {
		return false
	}

	desc, err := d.files.FindDescriptorByName(protoreflect.FullName(service))
	if err != nil {
		return false
	}

	svcDesc, ok := desc.(protoreflect.ServiceDescriptor)
	if !ok {
		return false
	}

	return svcDesc.Methods().ByName(protoreflect.Name(method)) != nil
}
