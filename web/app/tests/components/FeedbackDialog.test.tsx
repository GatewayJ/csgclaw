import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { FeedbackDialog } from "@/pages/SettingsPage/components/FeedbackDialog";

const props = {
  open: true,
  busy: false,
  description: "",
  images: [],
  error: "",
  t: (key: string) => key,
  onOpenChange: vi.fn(),
  onDescriptionChange: vi.fn(),
  onImagesChange: vi.fn(),
  onSubmit: vi.fn(),
};

it("requires text or images and sends text changes to the controller", async () => {
  render(<FeedbackDialog {...props} />);
  expect(screen.getByRole("button", { name: "feedbackSubmit" })).toBeDisabled();
  await userEvent.setup().type(screen.getByRole("textbox"), "x");
  expect(props.onDescriptionChange).toHaveBeenCalledWith("x");
});

it("disables edits and submission while sending", () => {
  render(<FeedbackDialog {...props} description="Problem" busy />);
  expect(screen.getByRole("textbox")).toBeDisabled();
  expect(screen.getByRole("button", { name: "feedbackSubmit" })).toBeDisabled();
});
