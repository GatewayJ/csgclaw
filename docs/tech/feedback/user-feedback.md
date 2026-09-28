# User feedback

The Settings page offers Write feedback and the existing GitHub action. Write feedback uses the existing OpenCSG sign-in dialog; successful sign-in opens the feedback form. The form accepts text and up to six JPEG/PNG images, at most 5 MiB each. At least text or one image is required. Text is limited to 20,000 characters.

The page-owned `useFeedback` hook coordinates form state and submission recovery. `FeedbackDialog` renders controlled data and emits callbacks. `src/api/feedback.ts` owns HTTP calls; `src/models/feedback.ts` owns browser validation. The sidebar settings button navigates directly to Settings.

`POST /api/v1/feedback` validates the multipart request, reads current login credentials, and adds the running version, installed channel, and active OpenCSG site. `internal/feedback.Client` sends it to `/api/v1/csgbot/user-feedback` on that site's StarHub Server. GitLab credentials, project selection, attachments, and Issue creation belong to CSGBot. `GET /api/v1/feedback/{submission_id}` proxies authenticated submission status.

A successful response closes the form and displays confirmation. Failures retain form data. An uncertain network result locks editing and offers Check status; a missing submission can be resent with the same UUID and content. Clear failures allow editing and retry. Concurrent submit clicks share the existing operation. GitHub remains available without OpenCSG authentication.

Deploy the CSGBot feedback endpoints and persistent submission storage before enabling this client release. StarHub's existing authenticated CSGBot wildcard proxy handles the new paths; its ingress must permit the 31 MiB request limit. Test against each supported login site before release.
