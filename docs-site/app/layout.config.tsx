import type { BaseLayoutProps } from 'fumadocs-ui/layouts/shared';

export const baseOptions: BaseLayoutProps = {
  nav: {
    title: (
      <span className="inline-flex items-center gap-2 font-mono font-semibold tracking-tight">
        <img src="/icon.svg" alt="" width={24} height={24} className="size-6" />
        booksy-cli
      </span>
    ),
  },
  githubUrl: 'https://github.com/GodGMN/booksy-cli',
};
