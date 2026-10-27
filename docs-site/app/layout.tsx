import './global.css';
import { RootProvider } from 'fumadocs-ui/provider/next';
import type { ReactNode } from 'react';
import type { Metadata } from 'next';

export const metadata: Metadata = {
  metadataBase: new URL(process.env.SITE_URL ?? 'https://booksy.gimeno.dev'),
  title: { default: 'booksy-cli', template: '%s | booksy-cli' },
  description: 'Find open slots, watch for cancellations and book Booksy appointments from the terminal. JSON by default.',
  icons: { icon: '/icon.svg' },
};

export default function Layout({ children }: { children: ReactNode }) {
  return (
    <html lang="en" className="dark" suppressHydrationWarning>
      <body className="flex min-h-screen flex-col">
        <RootProvider theme={{ defaultTheme: 'dark' }} search={{ options: { type: 'static' } }}>
          {children}
        </RootProvider>
      </body>
    </html>
  );
}
