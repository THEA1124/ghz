package protodesc

import (
	"errors"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"

	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/protoc-gen-go/descriptor"
	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/desc/protoparse"
	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GetMethodDescFromProto gets method descritor for the given call symbol from proto file given my path proto
// imports is used for import paths in parsing the proto file
func GetMethodDescFromProto(call, proto string, imports []string) (*desc.MethodDescriptor, error) {
	p := &protoparse.Parser{ImportPaths: imports}

	filename := proto
	if filepath.IsAbs(filename) {
		filename = filepath.Base(proto)
	}

	fds, err := p.ParseFiles(filename)
	if err != nil {
		return nil, err
	}

	fileDesc := fds[0]

	files := map[string]*desc.FileDescriptor{}
	files[fileDesc.GetName()] = fileDesc

	return getMethodDesc(call, files)
}

// GetMethodDescFromProtoSet gets method descritor for the given call symbol from protoset file given my path protoset
func GetMethodDescFromProtoSet(call, protoset string) (*desc.MethodDescriptor, error) {
	b, err := ioutil.ReadFile(protoset)
	if err != nil {
		return nil, fmt.Errorf("could not load protoset file %q: %v", protoset, err)
	}

	var fds descriptor.FileDescriptorSet
	err = proto.Unmarshal(b, &fds)
	if err != nil {
		return nil, fmt.Errorf("could not parse contents of protoset file %q: %v", protoset, err)
	}

	unresolved := map[string]*descriptor.FileDescriptorProto{}
	for _, fd := range fds.File {
		unresolved[fd.GetName()] = fd
	}
	resolved := map[string]*desc.FileDescriptor{}
	for _, fd := range fds.File {
		_, err := resolveFileDescriptor(unresolved, resolved, fd.GetName())
		if err != nil {
			return nil, err
		}
	}

	return getMethodDesc(call, resolved)
}

// GetMethodDescFromReflect gets method descriptor for the call from reflection using client
func GetMethodDescFromReflect(call string, client *grpcreflect.Client) (*desc.MethodDescriptor, error) {
	file, err := client.FileContainingSymbol(call)
	if err != nil {
		return nil, reflectionSupport(err)
	}

	files := map[string]*desc.FileDescriptor{}
	files[file.GetName()] = file

	return getMethodDesc(call, files)
}

func getMethodDesc(call string, files map[string]*desc.FileDescriptor) (*desc.MethodDescriptor, error) {
	svc, mth := parseSymbol(call)
	if svc == "" || mth == "" {
		return nil, fmt.Errorf("given method name %q is not in expected format: 'service/method' or 'service.method'", call)
	}

	dsc, err := findServiceSymbol(files, svc)
	if err != nil {
		return nil, err
	}
	if dsc == nil {
		return nil, fmt.Errorf("cannot find service %q", svc)
	}

	sd, ok := dsc.(*desc.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("cannot find service %q", svc)
	}

	mtd := sd.FindMethodByName(mth)
	if mtd == nil {
		return nil, fmt.Errorf("service %q does not include a method named %q", svc, mth)
	}

	return mtd, nil
}

func resolveFileDescriptor(unresolved map[string]*descriptor.FileDescriptorProto, resolved map[string]*desc.FileDescriptor, filename string) (*desc.FileDescriptor, error) {
	if r, ok := resolved[filename]; ok {
		return r, nil
	}
	fd, ok := unresolved[filename]
	if !ok {
		return nil, fmt.Errorf("no descriptor found for %q", filename)
	}
	deps := make([]*desc.FileDescriptor, 0, len(fd.GetDependency()))
	for _, dep := range fd.GetDependency() {
		depFd, err := resolveFileDescriptor(unresolved, resolved, dep)
		if err != nil {
			return nil, err
		}
		deps = append(deps, depFd)
	}
	result, err := desc.CreateFileDescriptor(fd, deps...)
	if err != nil {
		return nil, err
	}
	resolved[filename] = result
	return result, nil
}

func findServiceSymbol(resolved map[string]*desc.FileDescriptor, fullyQualifiedName string) (desc.Descriptor, error) {
	for _, fd := range resolved {
		if dsc := fd.FindSymbol(fullyQualifiedName); dsc != nil {
			return dsc, nil
		}
	}
	return nil, fmt.Errorf("cannot find service %q", fullyQualifiedName)
}

func parseSymbol(svcAndMethod string) (string, string) {
	pos := strings.LastIndex(svcAndMethod, "/")
	if pos < 0 {
		pos = strings.LastIndex(svcAndMethod, ".")
		if pos < 0 {
			return "", ""
		}
	}
	return svcAndMethod[:pos], svcAndMethod[pos+1:]
}

func reflectionSupport(err error) error {
	if err == nil {
		return nil
	}
	if stat, ok := status.FromError(err); ok && stat.Code() == codes.Unimplemented {
		return errors.New("server does not support the reflection API")
	}
	return err
}
