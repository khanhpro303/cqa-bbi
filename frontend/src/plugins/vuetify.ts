import 'vuetify/styles'
import '@mdi/font/css/materialdesignicons.css'
import { createVuetify } from 'vuetify'

export default createVuetify({
  theme: {
    defaultTheme: 'light',
    themes: {
      light: {
        colors: {
          primary: '#D32F2F',      // BBI CRM Red
          secondary: '#E57373',
          accent: '#FF5252',
          success: '#4CAF50',
          warning: '#FF9800',
          error: '#F44336',
          info: '#2196F3',
          background: '#F5F5F5',
          surface: '#FFFFFF',
        },
      },
      dark: {
        colors: {
          primary: '#E53935',
          secondary: '#EF9A9A',
          accent: '#FF5252',
          background: '#121212',
          surface: '#1E1E1E',
        },
      },
    },
  },
  defaults: {
    VCard: {
      rounded: 'lg',
      elevation: 1,
    },
    VBtn: {
      rounded: 'lg',
    },
    VTextField: {
      variant: 'outlined',
      density: 'comfortable',
    },
    VSelect: {
      variant: 'outlined',
      density: 'comfortable',
    },
    VSwitch: {
      // Vuetify 4's default switch renders a 20px thumb over a 14px track, so the
      // thumb overhangs and looks clipped. `inset` gives a contained pill toggle.
      inset: true,
      color: 'primary',
    },
  },
})
