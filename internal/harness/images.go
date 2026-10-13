package harness

import (
	"encoding/base64"
	"fmt"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"google.golang.org/protobuf/proto"
)

const (
	maxProviderImages     = 8
	maxProviderImageBytes = 5 << 20
	maxProviderImageTotal = maxProviderImages * maxProviderImageBytes
)

// imageDataURI is the provider-neutral representation used by Codex's app-server image input. The bytes have
// already been validated and bounded by plan; these checks protect direct provider callers and keep malformed input
// from reaching a process.
func imageDataURI(attachment plan.AttachmentData, index int) (string, error) {
	if attachment.Metadata == nil {
		return "", fmt.Errorf("image attachment %d has no metadata", index+1)
	}
	mediaType := imageMediaType(attachment.Metadata)
	if mediaType != "image/png" && mediaType != "image/jpeg" && mediaType != "image/gif" {
		return "", fmt.Errorf("image attachment %d has unsupported media type %q", index+1, mediaType)
	}
	if len(attachment.Data) == 0 {
		return "", fmt.Errorf("image attachment %d has no data", index+1)
	}
	if len(attachment.Data) > maxProviderImageBytes {
		return "", fmt.Errorf("image attachment %d exceeds %d bytes", index+1, maxProviderImageBytes)
	}
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(attachment.Data), nil
}

func validateImageInputs(attachments []plan.AttachmentData) error {
	if len(attachments) > maxProviderImages {
		return fmt.Errorf("at most %d image attachments are supported", maxProviderImages)
	}
	var total int
	for n, attachment := range attachments {
		if _, err := imageDataURI(attachment, n); err != nil {
			return err
		}
		total += len(attachment.Data)
	}
	if total > maxProviderImageTotal {
		return fmt.Errorf("image attachments exceed %d bytes", maxProviderImageTotal)
	}
	return nil
}

func imageMediaType(metadata *planv1.Attachment) string {
	if metadata == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(metadata.GetMediaType()))
}

// cloneImageInputs keeps queued provider messages independent from the instruction store and caller-owned byte
// slices. Metadata is cloned as well so a later store update cannot change the wire payload while a turn waits.
func cloneImageInputs(attachments []plan.AttachmentData) []plan.AttachmentData {
	if len(attachments) == 0 {
		return nil
	}
	out := make([]plan.AttachmentData, len(attachments))
	for n, attachment := range attachments {
		out[n].Data = append([]byte(nil), attachment.Data...)
		if attachment.Metadata != nil {
			out[n].Metadata = proto.Clone(attachment.Metadata).(*planv1.Attachment)
		}
	}
	return out
}

func sendWorkerInput(worker Worker, text string, attachments []plan.AttachmentData) error {
	if len(attachments) == 0 {
		return worker.Send(text)
	}
	imageWorker, ok := worker.(ImageWorker)
	if !ok {
		return fmt.Errorf("%w: %T", ErrImageInputUnsupported, worker)
	}
	return imageWorker.SendWithAttachments(text, attachments)
}
