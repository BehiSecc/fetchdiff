package notifier

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
)

func createAttachmentPart(writer *multipart.Writer, field string, attachment Attachment) (io.Writer, error) {
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, field, safeAttachmentName(attachment.Name)))
	contentType := attachment.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	header.Set("Content-Type", contentType)
	return writer.CreatePart(header)
}
