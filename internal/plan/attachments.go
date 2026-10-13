package plan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // Register GIF with image.Decode.
	_ "image/jpeg" // Register JPEG with image.Decode.
	_ "image/png"  // Register PNG with image.Decode.
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

const (
	attachmentFolder    = "attachments"
	maxAttachmentBytes  = 5 << 20
	maxAttachmentCount  = 8
	maxAttachmentPixels = 40_000_000
	maxAttachmentWidth  = 10_000
	maxAttachmentHeight = 10_000
)

var allowedAttachmentTypes = map[string]bool{
	"image/gif":  true,
	"image/jpeg": true,
	"image/png":  true,
}

// preparedAttachment is validated input that has not yet been assigned a durable identifier.
type preparedAttachment struct {
	meta *planv1.Attachment
	data []byte
}

// AttachmentData is a validated image ready for a provider's typed input encoder. The metadata remains the
// instruction's ownership record; Data is a fresh bounded copy read from that record's private store.
type AttachmentData struct {
	Metadata *planv1.Attachment
	Data     []byte
}

// ValidateAttachmentInputs validates image bytes without writing them. It is useful for provider preflight; durable
// task and instruction indications should use StoreAttachmentInputs so their event or instruction owns immutable
// files. Returned slices are owned by the caller and contain no filesystem paths.
func ValidateAttachmentInputs(inputs []*planv1.AttachmentInput) ([]AttachmentData, error) {
	prepared, err := validateAttachmentInputs(inputs)
	if err != nil {
		return nil, err
	}
	out := make([]AttachmentData, len(prepared))
	for n, p := range prepared {
		out[n] = AttachmentData{Metadata: protoCloneAttachment(p.meta), Data: append([]byte(nil), p.data...)}
	}
	return out, nil
}

// StoreAttachmentInputs validates image bytes and writes them as immutable, UUID-owned files. The returned data is
// suitable for the provider's native image input, while IDs identify the files that the caller must remove if its
// durable event transaction does not commit. No request supplied path is ever opened by this helper.
func StoreAttachmentInputs(home string, inputs []*planv1.AttachmentInput) ([]AttachmentData, []string, error) {
	if len(inputs) == 0 {
		return nil, nil, nil
	}
	if strings.TrimSpace(home) == "" {
		return nil, nil, errors.New("image attachments require Djinn's data folder")
	}
	prepared, err := validateAttachmentInputs(inputs)
	if err != nil {
		return nil, nil, err
	}
	return storePreparedAttachments(home, prepared)
}

// StoreAttachmentPaths normalizes and validates explicit local image paths, then stores them with the same atomic
// file and metadata rules as typed window uploads. It is the CLI task indication path; callers must remove the
// returned IDs when their event transaction does not commit.
func StoreAttachmentPaths(home string, paths []string) ([]AttachmentData, []string, error) {
	if len(paths) == 0 {
		return nil, nil, nil
	}
	prepared, err := prepareAttachments(home, nil, paths)
	if err != nil {
		return nil, nil, err
	}
	return storePreparedAttachments(home, prepared)
}

func storePreparedAttachments(home string, prepared []preparedAttachment) ([]AttachmentData, []string, error) {
	if len(prepared) == 0 {
		return nil, nil, nil
	}
	metadata, _, err := placeAttachments(home, prepared)
	if err != nil {
		return nil, nil, err
	}
	ids := make([]string, len(metadata))
	out := make([]AttachmentData, len(metadata))
	for n, meta := range metadata {
		ids[n] = meta.GetId()
		out[n] = AttachmentData{Metadata: protoCloneAttachment(meta), Data: append([]byte(nil), prepared[n].data...)}
	}
	return out, ids, nil
}

// ReadAttachment resolves one stored attachment by its validated identifier. Callers must already have an
// instruction reference; the path is derived from the UUID and is never accepted from a request.
func ReadAttachment(home string, ref *planv1.Attachment) ([]byte, error) {
	if ref == nil {
		return nil, errors.New("attachment is empty")
	}
	if strings.TrimSpace(home) == "" {
		return nil, errors.New("image attachments require Djinn's data folder")
	}
	data, err := readStoredAttachment(home, ref.GetId())
	if err != nil {
		return nil, err
	}
	validated, err := validateAttachment(ref.GetName(), ref.GetMediaType(), data)
	if err != nil {
		return nil, fmt.Errorf("stored attachment %s: %w", ref.GetId(), err)
	}
	if validated.meta.GetSize() != ref.GetSize() || validated.meta.GetSha256() != ref.GetSha256() ||
		validated.meta.GetWidth() != ref.GetWidth() || validated.meta.GetHeight() != ref.GetHeight() ||
		validated.meta.GetMediaType() != ref.GetMediaType() {
		return nil, fmt.Errorf("stored attachment %s metadata changed", ref.GetId())
	}
	return data, nil
}

// ReadInstructionAttachments resolves every image in an instruction in declaration order. It is the bridge used by
// supported providers before building Claude/Codex image blocks; an invalid or missing blob fails the delivery.
func ReadInstructionAttachments(home string, instruction *planv1.Instruction) ([]AttachmentData, error) {
	if instruction == nil {
		return nil, errors.New("instruction is empty")
	}
	out := make([]AttachmentData, 0, len(instruction.GetAttachments()))
	for _, ref := range instruction.GetAttachments() {
		data, err := ReadAttachment(home, ref)
		if err != nil {
			return nil, err
		}
		out = append(out, AttachmentData{Metadata: protoCloneAttachment(ref), Data: data})
	}
	return out, nil
}

// ReadTaskEventAttachments resolves one persisted event's image metadata and bytes in declaration order. The event
// reference is the ownership check; callers never provide a filesystem path or arbitrary attachment identifier.
func ReadTaskEventAttachments(home string, event *planv1.TaskEvent) ([]*planv1.AttachmentExport, error) {
	if event == nil {
		return nil, errors.New("task event is empty")
	}
	if len(event.GetAttachments()) > maxAttachmentCount {
		return nil, fmt.Errorf("task event has more than %d image attachments", maxAttachmentCount)
	}
	out := make([]*planv1.AttachmentExport, 0, len(event.GetAttachments()))
	for _, ref := range event.GetAttachments() {
		if ref == nil || ref.GetId() == "" {
			return nil, errors.New("task event attachment has no identifier")
		}
		data, err := ReadAttachment(home, ref)
		if err != nil {
			return nil, err
		}
		out = append(out, &planv1.AttachmentExport{Attachment: protoCloneAttachment(ref), Data: data})
	}
	return out, nil
}

// prepareAttachments validates typed bytes and explicit local paths. Paths are accepted only through the CLI-only
// attachment_paths field; the browser's SendData RPC can provide bytes but never causes a server-side file read.
func prepareAttachments(home string, inputs []*planv1.AttachmentInput, paths []string) ([]preparedAttachment, error) {
	if len(inputs)+len(paths) > maxAttachmentCount {
		return nil, attachmentError(connect.CodeInvalidArgument, "at most %d images may be attached", maxAttachmentCount)
	}
	if len(inputs)+len(paths) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(home) == "" {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("image attachments require Djinn's data folder"))
	}
	inputPrepared, err := validateAttachmentInputs(inputs)
	if err != nil {
		return nil, attachmentError(connect.CodeInvalidArgument, "%v", err)
	}
	var out = append([]preparedAttachment(nil), inputPrepared...)
	var total int64
	for _, p := range inputPrepared {
		total += int64(len(p.data))
	}
	for n, rawPath := range paths {
		path, err := normalizeAttachmentPath(rawPath)
		if err != nil {
			return nil, attachmentError(connect.CodeInvalidArgument, "attachment path %d: %v", n+1, err)
		}
		data, err := readAttachmentFile(path)
		if err != nil {
			return nil, attachmentError(connect.CodeFailedPrecondition, "attachment path %q: %v", rawPath, err)
		}
		p, err := validateAttachment(filepath.Base(path), "", data)
		if err != nil {
			return nil, attachmentError(connect.CodeInvalidArgument, "attachment path %q: %v", rawPath, err)
		}
		total += int64(len(p.data))
		out = append(out, p)
	}
	// The aggregate bound keeps a single request from consuming an unbounded amount of memory while it is staged.
	if total > int64(maxAttachmentBytes)*int64(maxAttachmentCount) {
		return nil, attachmentError(connect.CodeInvalidArgument, "attached image data exceeds %d bytes", maxAttachmentBytes*maxAttachmentCount)
	}
	return out, nil
}

func validateAttachmentInputs(inputs []*planv1.AttachmentInput) ([]preparedAttachment, error) {
	if len(inputs) > maxAttachmentCount {
		return nil, fmt.Errorf("at most %d images may be attached", maxAttachmentCount)
	}
	var out []preparedAttachment
	var total int64
	for n, input := range inputs {
		if input == nil {
			return nil, fmt.Errorf("attachment %d is empty", n+1)
		}
		p, err := validateAttachment(input.GetName(), input.GetMediaType(), input.GetData())
		if err != nil {
			return nil, fmt.Errorf("attachment %d: %w", n+1, err)
		}
		total += int64(len(p.data))
		if total > int64(maxAttachmentBytes)*int64(maxAttachmentCount) {
			return nil, fmt.Errorf("attached image data exceeds %d bytes", maxAttachmentBytes*maxAttachmentCount)
		}
		out = append(out, p)
	}
	return out, nil
}

func attachmentError(code connect.Code, format string, args ...any) error {
	return connect.NewError(code, fmt.Errorf(format, args...))
}

func validateAttachment(name, declared string, data []byte) (preparedAttachment, error) {
	if err := validAttachmentName(name); err != nil {
		return preparedAttachment{}, err
	}
	if len(data) == 0 {
		return preparedAttachment{}, errors.New("image data is empty")
	}
	if len(data) > maxAttachmentBytes {
		return preparedAttachment{}, fmt.Errorf("image data is larger than %d MiB", maxAttachmentBytes>>20)
	}
	declared = strings.ToLower(strings.TrimSpace(declared))
	sniffed := http.DetectContentType(data)
	if !allowedAttachmentTypes[sniffed] {
		return preparedAttachment{}, fmt.Errorf("unsupported image type %q", sniffed)
	}
	if declared != "" && declared != sniffed {
		return preparedAttachment{}, fmt.Errorf("declared type %q does not match %q", declared, sniffed)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return preparedAttachment{}, fmt.Errorf("decode image: %w", err)
	}
	if config.Width < 1 || config.Height < 1 || config.Width > maxAttachmentWidth || config.Height > maxAttachmentHeight {
		return preparedAttachment{}, fmt.Errorf("image dimensions %dx%d exceed %dx%d", config.Width, config.Height, maxAttachmentWidth, maxAttachmentHeight)
	}
	if int64(config.Width)*int64(config.Height) > maxAttachmentPixels {
		return preparedAttachment{}, fmt.Errorf("image dimensions %dx%d exceed %d pixels", config.Width, config.Height, maxAttachmentPixels)
	}
	if formatType(format) != sniffed {
		return preparedAttachment{}, fmt.Errorf("image decoder format %q does not match %q", format, sniffed)
	}
	// Decode the complete first frame after bounding its dimensions. DecodeConfig alone accepts truncated image
	// headers, which would otherwise leave malformed bytes in the durable store and provider input.
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return preparedAttachment{}, fmt.Errorf("decode image data: %w", err)
	}
	sum := sha256.Sum256(data)
	return preparedAttachment{
		meta: &planv1.Attachment{Name: name, MediaType: sniffed, Size: int64(len(data)), Sha256: hex.EncodeToString(sum[:]), Width: uint32(config.Width), Height: uint32(config.Height)},
		data: append([]byte(nil), data...),
	}, nil
}

func formatType(format string) string {
	switch strings.ToLower(format) {
	case "png":
		return "image/png"
	case "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	default:
		return ""
	}
}

func validAttachmentName(name string) error {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
		return errors.New("name must be a single file name")
	}
	if !utf8.ValidString(name) {
		return errors.New("name is not valid UTF-8")
	}
	if utf8.RuneCountInString(name) > 255 {
		return errors.New("name is longer than 255 characters")
	}
	for _, r := range name {
		if unicode.IsControl(r) || r == '\u0000' {
			return errors.New("name contains a control character")
		}
	}
	return nil
}

// normalizeAttachmentPath resolves a caller-provided local path before it is opened. It never accepts a directory,
// a non-regular file or a path that still contains a traversal component after cleaning and symlink resolution.
func normalizeAttachmentPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("path is empty")
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	resolved = filepath.Clean(resolved)
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("path is not a regular file")
	}
	if info.Size() < 1 || info.Size() > maxAttachmentBytes {
		return "", fmt.Errorf("file size must be between 1 and %d bytes", maxAttachmentBytes)
	}
	// Keep the normalized absolute spelling in diagnostics and make the path check explicit for callers that inspect
	// this boundary. The reader also rejects symlink files and rechecks the opened descriptor's type and size.
	if !filepath.IsAbs(abs) || !filepath.IsAbs(resolved) {
		return "", errors.New("path is not absolute")
	}
	return resolved, nil
}

func readAttachmentFile(path string) ([]byte, error) {
	lstat, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if lstat.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("stored attachment is a symbolic link")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("path is not a regular file")
	}
	if info.Size() < 1 || info.Size() > maxAttachmentBytes {
		return nil, fmt.Errorf("file size must be between 1 and %d bytes", maxAttachmentBytes)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxAttachmentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > maxAttachmentBytes {
		return nil, fmt.Errorf("file size must be between 1 and %d bytes", maxAttachmentBytes)
	}
	return data, nil
}

// readStoredAttachment opens an attachment relative to its private directory. os.Root keeps every path component
// beneath that directory across races, while Lstat rejects even an in-root final symlink instead of silently
// accepting a different file.
func readStoredAttachment(home, id string) ([]byte, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, fmt.Errorf("attachment %q: invalid identifier", id)
	}
	root, err := openAttachmentRoot(home)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	linkInfo, err := root.Lstat(id)
	if err != nil {
		return nil, err
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("stored attachment is a symbolic link")
	}
	if !linkInfo.Mode().IsRegular() {
		return nil, errors.New("stored attachment is not a regular file")
	}
	if linkInfo.Size() < 1 || linkInfo.Size() > maxAttachmentBytes {
		return nil, fmt.Errorf("file size must be between 1 and %d bytes", maxAttachmentBytes)
	}
	f, err := root.Open(id)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("stored attachment is not a regular file")
	}
	if info.Size() < 1 || info.Size() > maxAttachmentBytes {
		return nil, fmt.Errorf("file size must be between 1 and %d bytes", maxAttachmentBytes)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxAttachmentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > maxAttachmentBytes {
		return nil, fmt.Errorf("file size must be between 1 and %d bytes", maxAttachmentBytes)
	}
	return data, nil
}

func attachmentRoot(home string) string { return filepath.Join(home, attachmentFolder) }

// openAttachmentRoot rejects a symlinked store before opening a descriptor. OpenRoot then keeps subsequent reads,
// writes and deletes beneath the directory even if an attacker renames a parent while the operation is in flight.
func openAttachmentRoot(home string) (*os.Root, error) {
	if strings.TrimSpace(home) == "" {
		return nil, errors.New("image attachments require Djinn's data folder")
	}
	rootPath := attachmentRoot(home)
	rootInfo, err := os.Lstat(rootPath)
	if err != nil {
		return nil, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("attachment store is a symbolic link")
	}
	if !rootInfo.IsDir() {
		return nil, errors.New("attachment store is not a directory")
	}
	return os.OpenRoot(rootPath)
}

func attachmentPath(home, id string) (string, error) {
	if _, err := uuid.Parse(id); err != nil {
		return "", fmt.Errorf("attachment %q: invalid identifier", id)
	}
	root := attachmentRoot(home)
	path := filepath.Join(root, id)
	if filepath.Dir(path) != root || filepath.Clean(path) != path {
		return "", errors.New("attachment path escapes its store")
	}
	return path, nil
}

// placeAttachments atomically creates each image while the caller's store transaction is open. The returned IDs are
// journaled and referenced by the Instruction in that same transaction. The caller removes them if the transaction
// rolls back.
func placeAttachments(home string, prepared []preparedAttachment) ([]*planv1.Attachment, []string, error) {
	if len(prepared) == 0 {
		return nil, nil, nil
	}
	if err := os.MkdirAll(attachmentRoot(home), 0o700); err != nil {
		return nil, nil, err
	}
	var metas []*planv1.Attachment
	var placed []string
	cleanup := func() {
		removeAttachmentFiles(home, placedIDs(placed))
	}
	for _, p := range prepared {
		meta := protoCloneAttachment(p.meta)
		meta.Id = store.NewID()
		path, err := attachmentPath(home, meta.GetId())
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		if err := writeAttachmentData(home, meta.GetId(), p.data); err != nil {
			cleanup()
			return nil, nil, err
		}
		placed = append(placed, path)
		metas = append(metas, meta)
	}
	return metas, placed, nil
}

func writeAttachmentData(home, id string, data []byte) error {
	if _, err := attachmentPath(home, id); err != nil {
		return err
	}
	if len(data) == 0 || len(data) > maxAttachmentBytes {
		return fmt.Errorf("attachment data must be between 1 and %d bytes", maxAttachmentBytes)
	}
	if err := os.MkdirAll(attachmentRoot(home), 0o700); err != nil {
		return err
	}
	root, err := openAttachmentRoot(home)
	if err != nil {
		return err
	}
	defer root.Close()
	tmpName := ".tmp-attachment-" + store.NewID()
	tmp, err := root.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = root.Remove(tmpName)
		}
	}()
	if _, err = io.Copy(tmp, bytes.NewReader(data)); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = root.Rename(tmpName, id); err != nil {
		return err
	}
	removeTemp = false
	return nil
}

func stageExportAttachments(home string, exp *planv1.WishExport) ([]string, error) {
	if len(exp.GetAttachments()) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(home) == "" {
		return nil, errors.New("image attachments require Djinn's data folder")
	}
	var paths []string
	for _, exported := range exp.GetAttachments() {
		if exported == nil || exported.GetAttachment() == nil {
			removeAttachmentFiles(home, placedIDs(paths))
			return nil, errors.New("attachment export is empty")
		}
		if err := writeAttachmentData(home, exported.GetAttachment().GetId(), exported.GetData()); err != nil {
			removeAttachmentFiles(home, placedIDs(paths))
			return nil, err
		}
		path, _ := attachmentPath(home, exported.GetAttachment().GetId())
		paths = append(paths, path)
	}
	return paths, nil
}

func protoCloneAttachment(a *planv1.Attachment) *planv1.Attachment {
	if a == nil {
		return nil
	}
	return &planv1.Attachment{Id: a.GetId(), Name: a.GetName(), MediaType: a.GetMediaType(), Size: a.GetSize(), Sha256: a.GetSha256(), Width: a.GetWidth(), Height: a.GetHeight()}
}

func removeAttachmentFiles(home string, ids []string) {
	root, err := openAttachmentRoot(home)
	if err != nil {
		return
	}
	defer root.Close()
	for _, id := range ids {
		if _, err := uuid.Parse(id); err == nil {
			_ = root.Remove(id)
		}
	}
}

// RemoveStoredAttachments removes only UUID-named files beneath Djinn's attachment store. It is the rollback hook
// for callers that stage bytes before their own event or instruction transaction commits.
func RemoveStoredAttachments(home string, ids []string) { removeAttachmentFiles(home, ids) }

// AttachmentIDsForEvents returns the unique stored image IDs owned by task events. It is used by task and wish
// deletion paths so event-owned blobs do not outlive their metadata records.
func AttachmentIDsForEvents(events []*planv1.TaskEvent) []string {
	seen := map[string]bool{}
	var ids []string
	for _, event := range events {
		for _, a := range event.GetAttachments() {
			if a.GetId() != "" && !seen[strings.ToLower(a.GetId())] {
				seen[strings.ToLower(a.GetId())] = true
				ids = append(ids, a.GetId())
			}
		}
	}
	return ids
}

func attachmentIDs(instructions []*planv1.Instruction) []string {
	seen := map[string]bool{}
	var ids []string
	for _, instruction := range instructions {
		for _, a := range instruction.GetAttachments() {
			if a.GetId() != "" && !seen[strings.ToLower(a.GetId())] {
				seen[strings.ToLower(a.GetId())] = true
				ids = append(ids, a.GetId())
			}
		}
	}
	return ids
}

func attachmentInputsEqual(instruction *planv1.Instruction, prepared []preparedAttachment) bool {
	old := instruction.GetAttachments()
	if len(old) != len(prepared) {
		return false
	}
	for n, p := range prepared {
		if old[n].GetName() != p.meta.GetName() || old[n].GetMediaType() != p.meta.GetMediaType() || old[n].GetSize() != p.meta.GetSize() ||
			old[n].GetSha256() != p.meta.GetSha256() || old[n].GetWidth() != p.meta.GetWidth() || old[n].GetHeight() != p.meta.GetHeight() {
			return false
		}
	}
	return true
}

func sameAttachmentMetadata(a, b *planv1.Attachment) bool {
	return a != nil && b != nil && a.GetId() == b.GetId() && a.GetName() == b.GetName() && a.GetMediaType() == b.GetMediaType() &&
		a.GetSize() == b.GetSize() && a.GetSha256() == b.GetSha256() && a.GetWidth() == b.GetWidth() && a.GetHeight() == b.GetHeight()
}
