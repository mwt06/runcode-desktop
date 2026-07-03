import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import rehypeHighlight from 'rehype-highlight'

// Markdown renders assistant text as GitHub-flavored Markdown with code syntax
// highlighting, styled to match the app theme. Block code keeps the highlight.js
// theme colors; inline code gets a compact chip.
export function Markdown({ children }: { children: string }) {
  return (
    <div className="mdx">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[[rehypeHighlight, { detect: false, ignoreMissing: true }]]}
        components={{
          p: (props) => <p className="my-2 first:mt-0 last:mb-0 leading-[1.75]" {...props} />,
          ul: (props) => <ul className="my-2 pl-5 list-disc space-y-1" {...props} />,
          ol: (props) => <ol className="my-2 pl-5 list-decimal space-y-1" {...props} />,
          li: (props) => <li className="leading-[1.7]" {...props} />,
          strong: (props) => <strong className="font-semibold text-ink" {...props} />,
          a: (props) => <a className="text-primaryink underline underline-offset-2" target="_blank" rel="noreferrer" {...props} />,
          h1: (props) => <h1 className="text-[18px] font-bold mt-3 mb-1.5" {...props} />,
          h2: (props) => <h2 className="text-[16px] font-bold mt-3 mb-1.5" {...props} />,
          h3: (props) => <h3 className="text-[15px] font-semibold mt-2.5 mb-1" {...props} />,
          blockquote: (props) => <blockquote className="border-l-2 border-line2 pl-3 my-2 text-muted" {...props} />,
          hr: () => <hr className="my-3 border-line" />,
          table: (props) => <table className="my-2 border-collapse text-[13px] block overflow-auto" {...props} />,
          th: (props) => <th className="border border-line2 px-2 py-1 bg-surface2 text-left" {...props} />,
          td: (props) => <td className="border border-line2 px-2 py-1" {...props} />,
          pre: (props) => <pre className="my-2 bg-inset border border-line2 rounded-lg p-3 overflow-auto text-[12.5px] font-mono leading-[1.55]" {...props} />,
          code: ({ className, children, ...rest }) => {
            const text = String(children ?? '')
            const isBlock = /language-/.test(className || '') || text.includes('\n')
            if (isBlock) {
              return <code className={className} {...rest}>{children}</code>
            }
            return (
              <code className="bg-inset text-[#7c3aed] px-1.5 py-0.5 rounded text-[0.9em] font-mono break-words" {...rest}>
                {children}
              </code>
            )
          },
        }}
      >
        {children}
      </ReactMarkdown>
    </div>
  )
}
