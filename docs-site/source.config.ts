import { defineDocs, defineConfig } from 'fumadocs-mdx/config';

export const docs = defineDocs({ dir: 'content/docs' });

export default defineConfig({
  mdxOptions: {
    // Shiki themes chosen to sit on the true-black ground the site uses.
    rehypeCodeOptions: {
      themes: { light: 'github-light', dark: 'github-dark-default' },
    },
  },
});
