import { useEffect, useState } from "react";
import {
  Button,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogRoot,
  DialogTitle,
} from "@/components/ui";
import type { TranslateFn } from "@/models/conversations";
import { MAX_FEEDBACK_DESCRIPTION_LENGTH, MAX_FEEDBACK_IMAGES } from "@/models/feedback";
import styles from "./FeedbackDialog.module.css";

type Props = {
  open: boolean;
  busy: boolean;
  locked: boolean;
  checking: boolean;
  description: string;
  images: File[];
  error: string;
  t: TranslateFn;
  onOpenChange: (open: boolean) => void;
  onDescriptionChange: (value: string) => void;
  onImagesChange: (value: File[]) => void;
  onSubmit: () => void;
};

export function FeedbackDialog(props: Props) {
  const { t, images, busy, locked } = props;
  return (
    <DialogRoot
      open={props.open}
      onOpenChange={(open) => {
        if (!busy) props.onOpenChange(open);
      }}
    >
      <DialogContent className={styles.dialog}>
        <DialogHeader>
          <DialogTitle>{t("feedbackTitle")}</DialogTitle>
          <DialogDescription>{t("settingsFeedbackDescription")}</DialogDescription>
        </DialogHeader>
        <DialogBody className={styles.body}>
          <label className={styles.label}>
            {t("settingsFeedbackLabel")}
            <textarea
              value={props.description}
              disabled={busy || locked}
              rows={6}
              maxLength={MAX_FEEDBACK_DESCRIPTION_LENGTH}
              placeholder={t("settingsFeedbackPlaceholder")}
              onChange={(event) => props.onDescriptionChange(event.target.value)}
            />
          </label>
          <label className={styles.label}>
            {t("feedbackImages")} ({images.length}/{MAX_FEEDBACK_IMAGES})
            <input
              type="file"
              accept="image/jpeg,image/png"
              multiple
              disabled={busy || locked}
              onChange={(event) => {
                props.onImagesChange([...images, ...Array.from(event.target.files || [])]);
                event.target.value = "";
              }}
            />
          </label>
          <p>{t("feedbackImageLimit")}</p>
          <div className={styles.images}>
            {images.map((file, index) => (
              <div className={styles.image} key={`${index}-${file.name}`}>
                <Preview file={file} />
                <Button
                  variant="secondaryGray"
                  size="sm"
                  disabled={busy || locked}
                  aria-label={`${t("feedbackRemoveImage")} ${index + 1}`}
                  onClick={() => props.onImagesChange(images.filter((_, position) => position !== index))}
                >
                  {t("feedbackRemoveImage")}
                </Button>
              </div>
            ))}
          </div>
          {props.error ? <p role="alert">{props.error}</p> : null}
        </DialogBody>
        <DialogFooter>
          <Button variant="secondaryGray" size="md" disabled={busy} onClick={() => props.onOpenChange(false)}>
            {t("close")}
          </Button>
          <Button
            variant="primary"
            size="md"
            loading={busy}
            disabled={busy || (!props.description.trim() && !images.length)}
            onClick={props.onSubmit}
          >
            {t(props.checking ? "feedbackCheckStatus" : "feedbackSubmit")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </DialogRoot>
  );
}

function Preview({ file }: { file: File }) {
  const [url, setURL] = useState("");
  useEffect(() => {
    const next = URL.createObjectURL(file);
    setURL(next);
    return () => URL.revokeObjectURL(next);
  }, [file]);
  return <img src={url || undefined} alt={file.name} />;
}
