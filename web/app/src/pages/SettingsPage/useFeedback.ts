import { useEffect, useRef, useState } from "react";
import { fetchFeedbackStatus, submitFeedback } from "@/api/feedback";
import type { FeedbackResult } from "@/api/feedback";
import type { TranslateFn } from "@/models/conversations";
import { feedbackImagesValid } from "@/models/feedback";
import { localizeAPIError } from "@/shared/i18n";

export function useFeedback(t: TranslateFn, accountScope = "") {
  const [description, setDescription] = useState("");
  const [images, setImages] = useState<File[]>([]);
  const [busy, setBusy] = useState(false);
  const [locked, setLocked] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState(false);
  const submissionID = useRef("");
  const inFlight = useRef(false);
  const retryAllowed = useRef(false);
  const generation = useRef(0);

  useEffect(() => {
    generation.current += 1;
    submissionID.current = "";
    inFlight.current = false;
    retryAllowed.current = false;
    setDescription("");
    setImages([]);
    setBusy(false);
    setLocked(false);
    setError("");
    setSuccess(false);
    return () => {
      generation.current += 1;
    };
  }, [accountScope]);

  function applyResult(result: FeedbackResult): boolean {
    if (result.success && result.state === "succeeded") {
      setSuccess(true);
      setDescription("");
      setImages([]);
      setLocked(false);
      setError("");
      submissionID.current = "";
      return true;
    }
    if (result.state === "failed") {
      setLocked(false);
      setError(t("feedbackRetry"));
    } else {
      retryAllowed.current = result.state === "not_found";
      setError(t(result.state === "not_found" ? "feedbackRetry" : "feedbackPending"));
    }
    return false;
  }

  async function submit(): Promise<boolean> {
    if (inFlight.current) return false;
    const currentGeneration = generation.current;
    inFlight.current = true;
    setBusy(true);
    setSuccess(false);
    setError("");
    const id = submissionID.current || crypto.randomUUID();
    submissionID.current = id;
    try {
      if (locked && !retryAllowed.current) {
        const result = await fetchFeedbackStatus(id);
        return currentGeneration === generation.current ? applyResult(result) : false;
      }
      setLocked(true);
      retryAllowed.current = false;
      const result = await submitFeedback(id, description, images);
      return currentGeneration === generation.current ? applyResult(result) : false;
    } catch (cause) {
      if (currentGeneration !== generation.current) return false;
      const status = (cause as { status?: number })?.status;
      if (status && [400, 401, 403, 409, 422].includes(status)) {
        setLocked(false);
        setError(localizeAPIError(cause, t, t("feedbackFailed")));
      } else {
        setError(t("feedbackPending"));
      }
      return false;
    } finally {
      if (currentGeneration === generation.current) {
        inFlight.current = false;
        setBusy(false);
      }
    }
  }

  function changeDescription(value: string) {
    if (locked || inFlight.current) return;
    submissionID.current = "";
    setSuccess(false);
    setDescription(value);
  }

  function changeImages(value: File[]) {
    if (locked || inFlight.current) return;
    if (!feedbackImagesValid(value)) {
      setError(t("feedbackImageLimit"));
      return;
    }
    submissionID.current = "";
    setError("");
    setSuccess(false);
    setImages(value);
  }

  return {
    description,
    images,
    busy,
    locked,
    error,
    success,
    submit,
    changeDescription,
    changeImages,
    checking: locked && !retryAllowed.current,
  };
}
