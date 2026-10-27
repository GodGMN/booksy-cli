import { source } from '@/lib/source';
import { llms } from 'fumadocs-core/source';

export const revalidate = false;
export const dynamic = 'force-static';

export async function GET(): Promise<Response> {
  return new Response(await llms(source).index(), {
    headers: { 'content-type': 'text/plain; charset=utf-8' },
  });
}
