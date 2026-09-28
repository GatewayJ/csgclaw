# User feedback

The Settings page offers Write feedback and the existing GitHub action. Write feedback requires an active OpenCSG login; a successful login resumes opening the form. The browser accepts text and up to six JPG/JPEG/PNG images of at most 5 MiB each.

The page-owned `useFeedback` hook coordinates form state and submission. `FeedbackDialog` renders controlled data and emits callbacks. `src/api/feedback.ts` owns HTTP calls; `src/models/feedback.ts` owns browser validation. The sidebar Settings button navigates directly to Settings.

`POST /api/v1/feedback` validates the multipart request, reads current login credentials, and supplies the running version, installed channel, username, and active OpenCSG site. `internal/feedback.Client` uploads images through that site's existing `/internal_api/upload?max_size=30m` endpoint using multipart field `file`. The portal recognizes the `30m` setting; local validation restricts every image to 5 MiB. Its returned `url` values become `screenshot_urls` in the JSON request to `/api/v1/csgbot/user-feedback` through StarHub. The portal's storage-proxy mode forwards the `user_token` cookie as a Bearer credential, so the client supplies the active account access token. Redirects are rejected to keep credentials on their intended hosts.

CSGBot's existing feedback service creates the Issue. Optional `csgclaw` context contains `site_url`, `version`, and `channel` for title and label generation. Existing JSON callers keep their behavior. GitLab credentials and project selection remain in CSGBot; image storage uses the portal's existing upload configuration.

A successful response closes the form and displays confirmation. Failures retain text and selected images. The browser prevents simultaneous submits and allows an explicit retry. An uncertain network outcome can result in a duplicate Issue if the user retries. The form is cleared when the active account/site changes; navigating away or reloading discards unsaved content. GitHub feedback remains available without OpenCSG authentication.

Deploy the CSGBot optional context support before releasing this client so the CSGClaw title and label rules are applied. StarHub uses its existing authenticated CSGBot proxy. Real-site upload and submission integration remains to be verified.
