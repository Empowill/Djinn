//go:build ignore

// gen writes the push of each <case>.txtpb of the folders here, a WishPush in text, in <case>.binpb: as the store of
// the build the folder names (a tag or a commit) wrote it, marshalled with that build's schema (its gen/djinn.binpb,
// read with git), deterministic, never with this build's. TestMigratePushes_OldStore reads them (W65). Run it from the
// repository's root:
//
//	go run ./internal/harness/testdata/oldpush/gen.go
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	_ "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	_ "google.golang.org/protobuf/types/known/anypb"
	_ "google.golang.org/protobuf/types/known/durationpb"
	_ "google.golang.org/protobuf/types/known/timestamppb"
)

const dir = "internal/harness/testdata/oldpush"

func main() {
	builds, err := os.ReadDir(dir)
	if err != nil {
		fail(err)
	}
	for _, b := range builds {
		if !b.IsDir() {
			continue
		}
		push, err := wishPush(b.Name())
		if err != nil {
			fail(fmt.Errorf("the schema of %s: %w", b.Name(), err))
		}
		cases, err := filepath.Glob(filepath.Join(dir, b.Name(), "*.txtpb"))
		if err != nil {
			fail(err)
		}
		for _, c := range cases {
			if strings.HasSuffix(c, ".want.txtpb") {
				continue
			}
			text, err := os.ReadFile(c)
			if err != nil {
				fail(err)
			}
			m := dynamicpb.NewMessage(push)
			if err := prototext.Unmarshal(text, m); err != nil {
				fail(fmt.Errorf("%s: %w", c, err))
			}
			out, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
			if err != nil {
				fail(fmt.Errorf("%s: %w", c, err))
			}
			if err := os.WriteFile(strings.TrimSuffix(c, ".txtpb")+".binpb", out, 0o600); err != nil {
				fail(err)
			}
		}
	}
}

// wishPush is the WishPush of the schema at rev; the files it imports from outside Djinn's own are this build's.
func wishPush(rev string) (protoreflect.MessageDescriptor, error) {
	raw, err := exec.Command("git", "show", rev+":gen/djinn.binpb").Output()
	if err != nil {
		return nil, err
	}
	set := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(raw, set); err != nil {
		return nil, err
	}
	own := map[string]*descriptorpb.FileDescriptorProto{}
	for _, f := range set.GetFile() {
		own[f.GetName()] = f
	}
	files := &protoregistry.Files{}
	var add func(name string) error
	add = func(name string) error {
		if _, err := files.FindFileByPath(name); err == nil {
			return nil
		}
		f, ok := own[name]
		if !ok {
			fd, err := protoregistry.GlobalFiles.FindFileByPath(name)
			if err != nil {
				return err
			}
			f = protodesc.ToFileDescriptorProto(fd)
		}
		for _, dep := range f.GetDependency() {
			if err := add(dep); err != nil {
				return err
			}
		}
		fd, err := protodesc.NewFile(f, files)
		if err != nil {
			return err
		}
		return files.RegisterFile(fd)
	}
	for name := range own {
		if err := add(name); err != nil {
			return nil, err
		}
	}
	d, err := files.FindDescriptorByName("plan.v1.WishPush")
	if err != nil {
		return nil, err
	}
	return d.(protoreflect.MessageDescriptor), nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
