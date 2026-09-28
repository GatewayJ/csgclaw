import { get, request } from "@/api/client";

export type FeedbackResult = {
  success: boolean;
  state: "not_found" | "uploading" | "creating" | "failed" | "succeeded";
  issue_id?: number;
  issue_url?: string;
};

export function submitFeedback(id: string, description: string, images: File[]): Promise<FeedbackResult> {
  const body = new FormData();
  body.set("submission_id", id);
  body.set("problem_description", description);
  for (const image of images) body.append("images", image);
  return request("/api/v1/feedback", { method: "POST", body });
}

export function fetchFeedbackStatus(id: string): Promise<FeedbackResult> {
  return get(`/api/v1/feedback/${encodeURIComponent(id)}`);
}
