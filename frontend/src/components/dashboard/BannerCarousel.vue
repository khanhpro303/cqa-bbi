<template>
  <v-card class="banner-carousel mb-4 rounded-lg overflow-hidden" elevation="2">
    <div @mouseenter="paused = true" @mouseleave="paused = false">
      <v-carousel
        v-model="slide"
        :height="bannerHeight"
        :cycle="!paused"
        :interval="5000"
        progress="primary"
        hide-delimiter-background
        show-arrows="hover"
      >
        <v-carousel-item
          v-for="banner in banners"
          :key="banner.src"
          :src="banner.src"
          :alt="banner.alt"
          cover
        />
      </v-carousel>
    </div>
  </v-card>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useDisplay } from 'vuetify'
import doiNguG20 from '../../assets/banners/doi-ngu-g20.jpg'
import doiNguBbi from '../../assets/banners/doi-ngu-bbi.jpg'
import khoArthur from '../../assets/banners/kho-arthur.jpg'

// Fixed-size banner: images are cropped (object-fit cover) to a consistent
// band so the carousel stays a tidy banner instead of stretching tall.
const banners = [
  { src: doiNguBbi, alt: 'Đội ngũ BBI' },
  { src: doiNguG20, alt: 'Đội ngũ G20' },
  { src: khoArthur, alt: 'Kho Arthur' },
]

const slide = ref(0)
const paused = ref(false)

const { smAndDown } = useDisplay()
const bannerHeight = computed(() => (smAndDown.value ? 160 : 220))
</script>

<style scoped>
.banner-carousel {
  /* Keep the banner readable as a band, never a full-bleed hero. */
  max-height: 220px;
}
</style>
