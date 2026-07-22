import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"

interface ConfirmActionDialogProps {
  open: boolean
  title: string
  description: string
  submitting?: boolean
  onOpenChange: (open: boolean) => void
  onConfirm: () => void
}

export function ConfirmActionDialog({
  open,
  title,
  description,
  submitting = false,
  onOpenChange,
  onConfirm,
}: ConfirmActionDialogProps) {
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={submitting}>取消</AlertDialogCancel>
          <AlertDialogAction disabled={submitting} onClick={onConfirm}>
            {submitting ? "提交中..." : "确认操作"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
