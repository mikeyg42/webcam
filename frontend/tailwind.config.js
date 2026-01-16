/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        // Backgrounds (dark-to-light)
        bg: {
          primary: '#101010',
          elevated: '#1a1a1a',
          subtle: '#242424',
          hover: '#2a2a2a',
        },
        // Accent (lime/chartreuse)
        accent: {
          DEFAULT: '#c4ff58',
          muted: '#9fcc47',
          glow: 'rgba(196, 255, 88, 0.15)',
        },
        // Text
        text: {
          primary: '#ffffff',
          secondary: '#a3a3a3',
          tertiary: '#666666',
          inverse: '#101010',
        },
        // Semantic
        status: {
          success: '#4ade80',
          warning: '#fbbf24',
          error: '#ef4444',
          info: '#60a5fa',
        },
        // Borders
        border: {
          subtle: '#2a2a2a',
          DEFAULT: '#3a3a3a',
          accent: '#c4ff58',
        },
      },
      fontFamily: {
        // Body/UI - Martian Mono
        mono: [
          'Martian Mono',
          'JetBrains Mono',
          'SF Mono',
          'Monaco',
          'Consolas',
          'monospace',
        ],
        // Headers - Space Grotesk
        display: [
          'Space Grotesk',
          'Inter',
          'system-ui',
          'sans-serif',
        ],
        // Editorial - Fraunces
        editorial: [
          'Fraunces',
          'Georgia',
          'serif',
        ],
      },
      fontSize: {
        '2xs': ['0.625rem', { lineHeight: '0.875rem' }],
        xs: ['0.75rem', { lineHeight: '1rem' }],
        sm: ['0.875rem', { lineHeight: '1.25rem' }],
        base: ['1rem', { lineHeight: '1.5rem' }],
        lg: ['1.125rem', { lineHeight: '1.75rem' }],
        xl: ['1.25rem', { lineHeight: '1.75rem' }],
        '2xl': ['1.5rem', { lineHeight: '2rem' }],
        '3xl': ['1.875rem', { lineHeight: '2.25rem' }],
      },
      spacing: {
        '18': '4.5rem',
        '22': '5.5rem',
      },
      borderRadius: {
        DEFAULT: '6px',
        lg: '10px',
        xl: '14px',
      },
      boxShadow: {
        glow: '0 0 20px rgba(196, 255, 88, 0.2)',
        'glow-sm': '0 0 10px rgba(196, 255, 88, 0.15)',
      },
      letterSpacing: {
        label: '0.05em',
      },
      animation: {
        'pulse-slow': 'pulse 3s cubic-bezier(0.4, 0, 0.6, 1) infinite',
        'spin-slow': 'spin 2s linear infinite',
      },
    },
  },
  plugins: [],
}
