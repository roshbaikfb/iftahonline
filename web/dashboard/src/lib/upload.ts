// postFile sends a file to a presigned POST (object storage): the
// policy's fields first, the file last, as S3 requires. It throws a
// message for the merchant when the storage refuses it.
export async function postFile(upload: { url: string; fields: Record<string, string> }, file: File, tooBig: string): Promise<void> {
  const form = new FormData();
  for (const [k, v] of Object.entries(upload.fields)) form.append(k, v);
  form.append('file', file);
  const res = await fetch(upload.url, { method: 'POST', body: form });
  if (!res.ok) throw new Error(tooBig);
}
