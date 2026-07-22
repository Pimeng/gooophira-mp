import { zodResolver } from "@hookform/resolvers/zod"
import { useState } from "react"
import { Controller, useForm } from "react-hook-form"
import { z } from "zod"

import { ConfirmActionDialog } from "@/components/ConfirmActionDialog"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { useBanRoomUser } from "@/hooks/useBanRoomUser"
import { useBanUser } from "@/hooks/useBanUser"
const userBanSchema = z.object({
  userId: z.number().int().positive("请输入有效的用户 ID"),
  banned: z.boolean(),
  disconnect: z.boolean(),
})

const roomBanSchema = z.object({
  userId: z.number().int().positive("请输入有效的用户 ID"),
  roomId: z.string().trim().min(1, "请输入房间 ID"),
  banned: z.boolean(),
})

type UserBanForm = z.infer<typeof userBanSchema>
type RoomBanForm = z.infer<typeof roomBanSchema>

function FormError({ message }: { message?: string }) {
  return message ? <p className="text-sm text-destructive">{message}</p> : null
}

export function BansPage() {
  const userMutation = useBanUser()
  const roomMutation = useBanRoomUser()
  const [userConfirmation, setUserConfirmation] = useState<UserBanForm | null>(null)
  const [roomConfirmation, setRoomConfirmation] = useState<RoomBanForm | null>(null)
  const [userSuccess, setUserSuccess] = useState(false)
  const [roomSuccess, setRoomSuccess] = useState(false)

  const userForm = useForm<UserBanForm>({
    resolver: zodResolver(userBanSchema),
    defaultValues: { userId: 0, banned: true, disconnect: false },
  })
  const roomForm = useForm<RoomBanForm>({
    resolver: zodResolver(roomBanSchema),
    defaultValues: { userId: 0, roomId: "", banned: true },
  })

  async function confirmUserBan(): Promise<void> {
    if (userConfirmation === null) return
    await userMutation.mutateAsync(userConfirmation)
    setUserConfirmation(null)
    setUserSuccess(true)
    userForm.reset()
  }

  async function confirmRoomBan(): Promise<void> {
    if (roomConfirmation === null) return
    await roomMutation.mutateAsync(roomConfirmation)
    setRoomConfirmation(null)
    setRoomSuccess(true)
    roomForm.reset()
  }

  return (
    <section aria-labelledby="bans-title" className="flex flex-col gap-6">
      <div>
        <p className="text-sm font-medium text-cyan-700">Administrative action</p>
        <h1 id="bans-title" className="mt-1 text-3xl font-semibold tracking-tight text-slate-950">封禁操作</h1>
        <p className="mt-2 max-w-2xl text-sm text-slate-500">这里仅提供封禁与解封操作，不展示封禁历史或操作记录。</p>
      </div>

      {userSuccess && <Alert><AlertTitle>用户封禁操作已提交</AlertTitle><AlertDescription>服务端已返回成功响应，用户列表将在重新获取后反映当前状态。</AlertDescription></Alert>}
      {roomSuccess && <Alert><AlertTitle>房间用户封禁操作已提交</AlertTitle><AlertDescription>服务端已返回成功响应。</AlertDescription></Alert>}
      {userMutation.error && <Alert variant="destructive"><AlertTitle>用户封禁操作失败</AlertTitle><AlertDescription>{userMutation.error.message}</AlertDescription></Alert>}
      {roomMutation.error && <Alert variant="destructive"><AlertTitle>房间用户封禁操作失败</AlertTitle><AlertDescription>{roomMutation.error.message}</AlertDescription></Alert>}

      <div className="grid gap-6 xl:grid-cols-2">
        <Card>
          <CardHeader><CardTitle>用户封禁</CardTitle><CardDescription>调用全局用户封禁接口，可选择立即断开连接。</CardDescription></CardHeader>
          <CardContent>
            <form className="flex flex-col gap-5" onSubmit={userForm.handleSubmit((values) => { setUserSuccess(false); setUserConfirmation(values) })}>
              <div className="flex flex-col gap-2"><Label htmlFor="user-ban-user-id">用户 ID</Label><Input id="user-ban-user-id" type="number" {...userForm.register("userId", { valueAsNumber: true })} /><FormError message={userForm.formState.errors.userId?.message} /></div>
              <Controller control={userForm.control} name="banned" render={({ field }) => <label className="flex items-start gap-3"><Checkbox checked={field.value} onCheckedChange={field.onChange} /><span><span className="block text-sm font-medium">执行封禁</span><span className="text-sm text-muted-foreground">关闭时执行解封。</span></span></label>} />
              <Controller control={userForm.control} name="disconnect" render={({ field }) => <label className="flex items-start gap-3"><Checkbox checked={field.value} onCheckedChange={field.onChange} /><span><span className="block text-sm font-medium">立即断开连接</span><span className="text-sm text-muted-foreground">仅用户封禁接口支持此选项。</span></span></label>} />
              <Button type="submit" disabled={userMutation.isPending}>提交用户封禁操作</Button>
            </form>
          </CardContent>
        </Card>

        <Card>
          <CardHeader><CardTitle>房间用户封禁</CardTitle><CardDescription>只影响指定房间中的指定用户，不会立即断开连接。</CardDescription></CardHeader>
          <CardContent>
            <form className="flex flex-col gap-5" onSubmit={roomForm.handleSubmit((values) => { setRoomSuccess(false); setRoomConfirmation(values) })}>
              <div className="flex flex-col gap-2"><Label htmlFor="room-ban-user-id">用户 ID</Label><Input id="room-ban-user-id" type="number" {...roomForm.register("userId", { valueAsNumber: true })} /><FormError message={roomForm.formState.errors.userId?.message} /></div>
              <div className="flex flex-col gap-2"><Label htmlFor="room-ban-room-id">房间 ID</Label><Input id="room-ban-room-id" {...roomForm.register("roomId")} /><FormError message={roomForm.formState.errors.roomId?.message} /></div>
              <Controller control={roomForm.control} name="banned" render={({ field }) => <label className="flex items-start gap-3"><Checkbox checked={field.value} onCheckedChange={field.onChange} /><span><span className="block text-sm font-medium">执行封禁</span><span className="text-sm text-muted-foreground">关闭时执行房间解封。</span></span></label>} />
              <Button type="submit" disabled={roomMutation.isPending}>提交房间封禁操作</Button>
            </form>
          </CardContent>
        </Card>
      </div>

      <ConfirmActionDialog open={userConfirmation !== null} onOpenChange={(open) => { if (!open && !userMutation.isPending) setUserConfirmation(null) }} onConfirm={() => void confirmUserBan()} submitting={userMutation.isPending} title={userConfirmation?.banned ? "确认封禁用户" : "确认解封用户"} description={userConfirmation === null ? "" : `将对用户 ID ${userConfirmation.userId} 执行${userConfirmation.banned ? "封禁" : "解封"}。${userConfirmation.disconnect ? "操作将立即断开该用户连接。" : "不会立即断开该用户连接。"}`} />
      <ConfirmActionDialog open={roomConfirmation !== null} onOpenChange={(open) => { if (!open && !roomMutation.isPending) setRoomConfirmation(null) }} onConfirm={() => void confirmRoomBan()} submitting={roomMutation.isPending} title={roomConfirmation?.banned ? "确认房间用户封禁" : "确认房间用户解封"} description={roomConfirmation === null ? "" : `将对用户 ID ${roomConfirmation.userId} 在房间 ${roomConfirmation.roomId} 执行${roomConfirmation.banned ? "封禁" : "解封"}。房间封禁接口不会立即断开连接。`} />
    </section>
  )
}
